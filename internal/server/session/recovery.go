package session

import (
	"fmt"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// RestoreGame validates before exposing any room or starting any goroutine.
func RestoreGame(record *storage.GameRecord, lb *storage.LeaderboardManager, factory func(SnapshotPlayer) types.ClientInterface) (*GameSession, *room.Room, error) {
	snapshot, err := DecodeSnapshot(record)
	if err != nil {
		return nil, nil, err
	}
	r := &room.Room{Code: record.RoomCode, CreatedAt: snapshot.CreatedAt, EndedAt: snapshot.EndedAt, Players: make(map[string]*room.RoomPlayer)}
	for _, player := range snapshot.Players {
		client := factory(player)
		if client == nil {
			return nil, nil, fmt.Errorf("cannot restore player %s", player.ID)
		}
		r.Players[player.ID] = &room.RoomPlayer{Client: client, Seat: player.Seat, Ready: true, IsLandlord: player.IsLandlord, Offline: !player.IsBot}
		r.PlayerOrder = append(r.PlayerOrder, player.ID)
	}
	gs := NewGameSession(r, lb, snapshot.Config)
	gs.loadSnapshot(snapshot)
	gs.gameID, gs.revision, gs.committed = record.ID, record.Revision, record
	if gs.allOfflineAt.IsZero() {
		gs.allOfflineAt = record.SavedAt
	}
	r.AllOfflineAt = gs.allOfflineAt
	for i, p := range gs.players {
		p.IsOffline = !snapshot.Players[i].IsBot
	}
	switch gs.state {
	case GameStateDealing:
		r.State = room.RoomStateDealing
	case GameStateBidding:
		r.State = room.RoomStateBidding
	case GameStatePlaying:
		r.State = room.RoomStatePlaying
	case GameStateEnded:
		r.State = room.RoomStateEnded
	}
	return gs, r, nil
}

// Resume starts at most the current deadline's timeout. It never simulates a
// backlog of turns for downtime; each subsequent turn gets its normal budget.
func (gs *GameSession) Resume() {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	gs.armPersistentTimers()
	if gs.timerMode != "turn" || !gs.timerStartTime.Add(gs.remainingTime).After(time.Now()) {
		return
	}
	gs.resumeBotTurn()
}

func (gs *GameSession) resumeBotTurn() {
	switch gs.state {
	case GameStateBidding:
		gs.broadcastBotTurn(codec.MustNewMessage(protocol.MsgBidTurn, protocol.BidTurnPayload{
			BidToken: gs.bidToken, PlayerID: gs.players[gs.currentBidder].ID,
			Timeout: gs.remainingTurnSeconds(gs.gameConfig.BidTimeout), IsGrab: gs.landlordCaller != -1, Multiplier: gs.bidMultiplier,
		}))
	case GameStatePlaying:
		player := gs.players[gs.currentPlayer]
		mustPlay := gs.lastPlayedHand.IsEmpty() || gs.lastPlayerIdx == gs.currentPlayer
		gs.broadcastBotTurn(codec.MustNewMessage(protocol.MsgPlayTurn, protocol.PlayTurnPayload{
			TurnToken: gs.playToken,
			PlayerID:  player.ID, Timeout: gs.remainingTurnSeconds(gs.gameConfig.TurnTimeout), MustPlay: mustPlay, CanBeat: true,
		}))
	}
}

// RestoreBot replays public information only, then sets the bot's own exact hand.
// Bot inference caches are derived state; no opponent private cards are supplied.
func (gs *GameSession) RestoreBot(client types.ClientInterface) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	players := make([]protocol.PlayerInfo, len(gs.players))
	counts := make(map[string]int)
	for i, p := range gs.players {
		players[i] = protocol.PlayerInfo{ID: p.ID, Name: p.Name, Seat: p.Seat, IsBot: gs.room.Players[p.ID].Client.IsBot()}
		counts[p.ID] = 17
	}
	client.SendMessage(codec.MustNewMessage(protocol.MsgGameStart, protocol.GameStartPayload{Players: players}))
	for _, bid := range gs.bidHistory {
		client.SendMessage(codec.MustNewMessage(protocol.MsgBidResult, bid))
	}
	for _, p := range gs.players {
		if p.IsLandlord {
			counts[p.ID] = 20
			client.SendMessage(codec.MustNewMessage(protocol.MsgLandlord, protocol.LandlordPayload{PlayerID: p.ID, PlayerName: p.Name, BottomCards: convert.CardsToInfos(gs.bottomCards), Multiplier: gs.bidMultiplier}))
		}
	}
	for _, p := range gs.players {
		if p.ID != client.GetID() {
			continue
		}
		hand := convert.CardsToInfos(p.Hand)
		for _, play := range gs.publicPlays {
			if play.PlayerID == p.ID {
				hand = append(hand, play.Cards...)
			}
		}
		client.SendMessage(codec.MustNewMessage(protocol.MsgDealCards, protocol.DealCardsPayload{Cards: hand}))
	}
	for _, play := range gs.publicPlays {
		if play.Type == "pass" {
			client.SendMessage(codec.MustNewMessage(protocol.MsgPlayerPass, protocol.PlayerPassPayload{PlayerID: play.PlayerID, PlayerName: play.PlayerName}))
		} else {
			counts[play.PlayerID] -= len(play.Cards)
			client.SendMessage(codec.MustNewMessage(protocol.MsgCardPlayed, protocol.CardPlayedPayload{PlayerID: play.PlayerID, PlayerName: play.PlayerName, Cards: play.Cards, CardsLeft: counts[play.PlayerID], HandType: play.HandType}))
		}
	}
	for _, p := range gs.players {
		if p.ID == client.GetID() {
			client.SendMessage(codec.MustNewMessage(protocol.MsgDealCards, protocol.DealCardsPayload{Cards: convert.CardsToInfos(p.Hand)}))
		}
	}
}
