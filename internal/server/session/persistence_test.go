package session

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/palemoky/fight-the-landlord/internal/bot"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"github.com/palemoky/fight-the-landlord/internal/types"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func persistentGame(t *testing.T) (game *GameSession, mr *miniredis.Miniredis, rdb *redis.Client) {
	t.Helper()
	mr = miniredis.RunT(t)
	rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	r := room.NewMockRoom("123456", nil)
	for i, id := range []string{"p1", "p2", "p3"} {
		r.Players[id] = &room.RoomPlayer{Client: testutil.NewSimpleClient(id, id), Seat: i, Ready: true}
		r.PlayerOrder = append(r.PlayerOrder, id)
	}
	game = NewGameSession(r, storage.NewLeaderboardManager(rdb), config.GameConfig{TurnTimeout: 600, BidTimeout: 600, OfflineWaitTimeout: 30})
	t.Cleanup(game.Suspend)
	game.Start()
	require.Nil(t, game.pending)
	return game, mr, rdb
}

func TestAuthorityRoundTripAcrossStagesAndDuplicateActions(t *testing.T) {
	gs, _, _ := persistentGame(t)
	assertRestores := func() {
		record, err := gs.store.LoadGame(context.Background(), gs.gameID)
		require.NoError(t, err)
		restored, _, err := RestoreGame(record, gs.leaderboard, func(p SnapshotPlayer) types.ClientInterface { return testutil.NewSimpleClient(p.ID, p.Name) })
		require.NoError(t, err)
		t.Cleanup(restored.Suspend)
		require.Equal(t, gs.gameID, restored.gameID)
		require.Equal(t, gs.revision, restored.revision)
		require.Equal(t, gs.state, restored.state)
		require.Equal(t, gs.bidToken, restored.bidToken)
		require.Equal(t, gs.actionReceipts, restored.actionReceipts)
		require.Equal(t, gs.playToken, restored.playToken)
		require.Equal(t, gs.bidHistory, restored.bidHistory)
		require.Equal(t, gs.publicPlays, restored.publicPlays)
		require.Equal(t, gs.lastPlayedHand, restored.lastPlayedHand)
		require.True(t, gs.timerStartTime.Equal(restored.timerStartTime))
		for i, p := range gs.players {
			require.Equal(t, p.Hand, restored.players[i].Hand)
		}
	}
	assertRestores()
	gs.finishDeal(gs.dealID)
	assertRestores()
	caller := gs.players[gs.currentBidder].ID
	token := gs.bidToken
	require.NoError(t, gs.HandleBidTurn(caller, true, token))
	require.Error(t, gs.HandleBidTurn(caller, true, token))
	require.Equal(t, token, gs.actionReceipts[caller])
	assertRestores()
	for gs.state == GameStateBidding {
		require.NoError(t, gs.HandleBidTurn(gs.players[gs.currentBidder].ID, false, gs.bidToken))
	}
	p := gs.players[gs.currentPlayer]
	playToken := gs.playToken
	cards := convert.CardsToInfos(p.Hand[:1])
	require.NoError(t, gs.HandlePlayCardsTurn(p.ID, cards, playToken))
	require.Error(t, gs.HandlePlayCardsTurn(p.ID, cards, playToken))
	require.Equal(t, playToken, gs.actionReceipts[p.ID])
	assertRestores()
	passer := gs.players[gs.currentPlayer].ID
	passToken := gs.playToken
	require.NoError(t, gs.HandlePassTurn(passer, passToken))
	require.Error(t, gs.HandlePassTurn(passer, passToken))
	assertRestores()
}

type lostGameResponse struct{ armed atomic.Bool }

func (h *lostGameResponse) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *lostGameResponse) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *lostGameResponse) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if err == nil && (cmd.Name() == "evalsha" || cmd.Name() == "eval") && h.armed.CompareAndSwap(true, false) {
			return errors.New("response interrupted after commit")
		}
		return err
	}
}

func TestPersistenceFailureWithholdsNotificationsAndRetriesExactCandidate(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "not_written", true: "response_lost"}[ambiguous], func(t *testing.T) {
			gs, mr, rdb := persistentGame(t)
			gs.finishDeal(gs.dealID)
			player := gs.players[gs.currentBidder].ID
			client := gs.room.Players[player].Client.(*testutil.SimpleClient)
			before := len(client.SentMessages())
			revision := gs.revision
			hook := &lostGameResponse{}
			rdb.AddHook(hook)
			if ambiguous {
				hook.armed.Store(true)
			} else {
				mr.SetError("ERR unavailable")
			}
			err := gs.HandleBidTurn(player, true, gs.bidToken)
			require.ErrorIs(t, err, ErrPersistenceUnavailable)
			require.Equal(t, before, len(client.SentMessages()), "no accepted action may escape before persistence confirmation")
			require.NotNil(t, gs.pending)
			candidate := gs.pending
			require.Nil(t, gs.turnTimer)
			require.Nil(t, gs.BuildGameStateDTO(player, NewSessionManager()))
			mr.SetError("")
			record, err := gs.store.LoadGame(context.Background(), gs.gameID)
			require.NoError(t, err)
			if ambiguous {
				require.EqualValues(t, revision+1, record.Revision)
			} else {
				require.Equal(t, revision, record.Revision)
			}
			require.Eventually(t, func() bool { gs.mu.RLock(); defer gs.mu.RUnlock(); return gs.pending == nil }, 3*time.Second, 5*time.Millisecond)
			record, err = gs.store.LoadGame(context.Background(), gs.gameID)
			require.NoError(t, err)
			require.Equal(t, candidate.State, record.State)
			require.Greater(t, len(client.SentMessages()), before)
		})
	}
}

func TestCrashAfterWriteBeforeAcknowledgementRecoversCommittedAction(t *testing.T) {
	gs, _, rdb := persistentGame(t)
	gs.finishDeal(gs.dealID)
	player, token := gs.players[gs.currentBidder].ID, gs.bidToken
	hook := &lostGameResponse{}
	hook.armed.Store(true)
	rdb.AddHook(hook)
	require.ErrorIs(t, gs.HandleBidTurn(player, true, token), ErrPersistenceUnavailable)
	gs.Suspend() // No retry and no outgoing accepted-action message.
	record, err := gs.store.LoadGame(context.Background(), gs.gameID)
	require.NoError(t, err)
	restored, _, err := RestoreGame(record, gs.leaderboard, func(p SnapshotPlayer) types.ClientInterface { return testutil.NewSimpleClient(p.ID, p.Name) })
	require.NoError(t, err)
	t.Cleanup(restored.Suspend)
	require.Equal(t, token, restored.actionReceipts[player])
	require.Len(t, restored.bidHistory, 1)
	require.True(t, restored.bidHistory[0].Bid)
	require.Error(t, restored.HandleBidTurn(player, true, token), "replaying the unacknowledged request must not apply it again")
}

func TestRecoveryExecutesOnlyOneExpiredTimeout(t *testing.T) {
	gs, _, _ := persistentGame(t)
	gs.finishDeal(gs.dealID)
	gs.mu.Lock()
	require.NoError(t, gs.transition(func() error { gs.timerStartTime = time.Now().Add(-time.Hour); return nil }))
	// Stop before allowing the freshly armed expired callback to acquire gs.mu.
	gs.stopped = true
	gs.stopPhysicalTimers()
	gs.mu.Unlock()
	record, err := gs.store.LoadGame(context.Background(), gs.gameID)
	require.NoError(t, err)
	restored, _, err := RestoreGame(record, gs.leaderboard, func(p SnapshotPlayer) types.ClientInterface { return testutil.NewSimpleClient(p.ID, p.Name) })
	require.NoError(t, err)
	t.Cleanup(restored.Suspend)
	restored.Resume()
	require.Eventually(t, func() bool { restored.mu.RLock(); defer restored.mu.RUnlock(); return restored.turnID > gs.turnID }, time.Second, time.Millisecond)
	restored.mu.RLock()
	defer restored.mu.RUnlock()
	require.Equal(t, gs.turnID+1, restored.turnID)
	require.Len(t, restored.bidHistory, 1)
	require.Greater(t, time.Until(restored.timerStartTime.Add(restored.remainingTime)), 9*time.Minute)
}

func TestTerminalSnapshotRecoversResultAndDurableStatistics(t *testing.T) {
	gs, _, _ := persistentGame(t)
	gs.finishDeal(gs.dealID)
	require.NoError(t, gs.HandleBid(gs.players[gs.currentBidder].ID, true))
	for gs.state == GameStateBidding {
		require.NoError(t, gs.HandleBid(gs.players[gs.currentBidder].ID, false))
	}
	for turn := 0; gs.state != GameStateEnded && turn < 300; turn++ {
		gs.handlePlayTimeout()
	}
	require.Equal(t, GameStateEnded, gs.state)
	record, err := gs.store.LoadGame(context.Background(), gs.gameID)
	require.NoError(t, err)
	require.True(t, record.Ended)
	require.Len(t, record.Results, 3)
	gs.Suspend()
	for range 3 {
		restored, _, err := RestoreGame(record, gs.leaderboard, func(p SnapshotPlayer) types.ClientInterface { return testutil.NewSimpleClient(p.ID, p.Name) })
		require.NoError(t, err)
		restored.Resume()
		require.Nil(t, restored.turnTimer)
		want, err := codec.ParsePayload[protocol.GameOverPayload](gs.lastResult)
		require.NoError(t, err)
		got, err := codec.ParsePayload[protocol.GameOverPayload](restored.lastResult)
		require.NoError(t, err)
		require.Equal(t, want, got)
		require.NoError(t, gs.store.DrainSettlements(context.Background(), gs.leaderboard))
		restored.Suspend()
	}
	for _, p := range gs.players {
		stats, err := gs.leaderboard.GetPlayerStats(context.Background(), p.ID)
		require.NoError(t, err)
		require.Equal(t, 1, stats.TotalGames)
	}
}

type recoveryBotEngine struct{ contexts chan bot.GameContext }

func (e *recoveryBotEngine) DecideBid(context.Context, string, []card.Card, *bool) bool { return false }
func (e *recoveryBotEngine) DecidePlay(_ context.Context, _ string, game bot.GameContext) []card.Card {
	e.contexts <- game
	return nil
}

func TestRestoredBotRebuildsOwnHandAndPublicHistory(t *testing.T) {
	gs, _, _ := persistentGame(t)
	gs.finishDeal(gs.dealID)
	require.NoError(t, gs.HandleBid(gs.players[gs.currentBidder].ID, true))
	for gs.state == GameStateBidding {
		require.NoError(t, gs.HandleBid(gs.players[gs.currentBidder].ID, false))
	}
	landlord := gs.players[gs.currentPlayer]
	require.NoError(t, gs.HandlePlayCards(landlord.ID, convert.CardsToInfos(landlord.Hand[:1])))
	p := gs.players[gs.currentPlayer]
	engine := &recoveryBotEngine{contexts: make(chan bot.GameContext, 1)}
	b := bot.RestoreClient(p.ID, "Persisted Bot", engine)
	gs.mu.Lock()
	gs.room.Players[p.ID].Client = b
	p.Name = b.GetName()
	require.NoError(t, gs.transition(func() error { return nil }))
	gs.mu.Unlock()
	record, err := gs.store.LoadGame(context.Background(), gs.gameID)
	require.NoError(t, err)
	gs.Suspend()
	var recoveredBot *bot.BotClient
	restored, _, err := RestoreGame(record, gs.leaderboard, func(p SnapshotPlayer) types.ClientInterface {
		if p.IsBot {
			recoveredBot = bot.RestoreClient(p.ID, p.Name, engine)
			return recoveredBot
		}
		return testutil.NewSimpleClient(p.ID, p.Name)
	})
	require.NoError(t, err)
	t.Cleanup(restored.Suspend)
	require.NotNil(t, recoveredBot)
	require.Equal(t, b.GetID(), recoveredBot.GetID())
	require.Equal(t, b.GetName(), recoveredBot.GetName())
	recoveredBot.SetSession(restored)
	restored.RestoreBot(recoveredBot)
	restored.Resume()
	select {
	case game := <-engine.contexts:
		require.Equal(t, p.Hand, game.Hand)
		require.Len(t, game.ActionSeq, 1)
		require.NotEmpty(t, game.LastMovePos)
	case <-time.After(3 * time.Second):
		t.Fatal("restored bot did not receive its turn")
	}
}

func TestPersistenceRetryRetainsDisconnectsAndRetentionDeadline(t *testing.T) {
	gs, mr, _ := persistentGame(t)
	gs.finishDeal(gs.dealID)
	revision := gs.revision
	mr.SetError("ERR unavailable")
	require.ErrorIs(t, gs.HandleBidTurn(gs.players[gs.currentBidder].ID, true, gs.bidToken), ErrPersistenceUnavailable)
	for _, p := range gs.players {
		gs.PlayerOffline(p.ID)
	}
	recoveredAt := time.Now()
	mr.SetError("")
	require.Eventually(t, func() bool { gs.mu.RLock(); defer gs.mu.RUnlock(); return gs.pending == nil }, 3*time.Second, time.Millisecond)
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	require.Equal(t, revision+2, gs.revision)
	require.False(t, gs.allOfflineAt.IsZero())
	require.False(t, gs.allOfflineAt.After(recoveredAt))
	require.Equal(t, gs.allOfflineAt, gs.room.AllOfflineAt)
	record, err := gs.store.LoadGame(context.Background(), gs.gameID)
	require.NoError(t, err)
	snapshot, err := DecodeSnapshot(record)
	require.NoError(t, err)
	for _, p := range snapshot.Players {
		require.True(t, p.IsOffline)
	}
	require.True(t, record.ExpiresAt.Equal(gs.allOfflineAt.Add(gs.gameConfig.OfflineRoomRetentionDuration())))
	require.Equal(t, "offline", gs.timerMode)
}

func TestPersistenceRetryAppliesOfflineThenOnlineWithLogicalTimer(t *testing.T) {
	gs, mr, _ := persistentGame(t)
	gs.finishDeal(gs.dealID)
	mr.SetError("ERR unavailable")
	require.ErrorIs(t, gs.HandleBidTurn(gs.players[gs.currentBidder].ID, true, gs.bidToken), ErrPersistenceUnavailable)
	next := gs.players[gs.currentBidder].ID
	gs.PlayerOffline(next)
	gs.PlayerOnline(next)
	mr.SetError("")
	require.Eventually(t, func() bool { gs.mu.RLock(); defer gs.mu.RUnlock(); return gs.pending == nil }, 3*time.Second, time.Millisecond)
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	require.False(t, gs.players[gs.currentBidder].IsOffline)
	require.Equal(t, "turn", gs.timerMode)
	require.NotNil(t, gs.turnTimer)
	require.Nil(t, gs.offlineWaitTimer)
}
