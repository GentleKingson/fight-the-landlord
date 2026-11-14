package room

import (
	"sync"
	"testing"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"github.com/palemoky/fight-the-landlord/internal/types"
	"github.com/stretchr/testify/require"
)

func fullWaitingRoom(t *testing.T) (*RoomManager, *Room, []*testutil.SimpleClient) {
	t.Helper()
	rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 600})
	clients := []*testutil.SimpleClient{testutil.NewSimpleClient("a", "A"), testutil.NewSimpleClient("b", "B"), testutil.NewSimpleClient("c", "C")}
	r, err := rm.CreateRoom(clients[0])
	require.NoError(t, err)
	for _, c := range clients[1:] {
		_, err = rm.JoinRoom(c, r.Code)
		require.NoError(t, err)
	}
	return rm, r, clients
}

func TestLeaveRejectsStartedRooms(t *testing.T) {
	for _, state := range []RoomState{RoomStateReady, RoomStateBidding, RoomStatePlaying, RoomStateEnded} {
		rm, r, clients := fullWaitingRoom(t)
		r.SetState(state)
		require.ErrorIs(t, rm.LeaveRoom(clients[0]), apperrors.ErrGameStarted)
		require.Equal(t, r.Code, clients[0].GetRoom())
		require.Len(t, r.Players, 3)
		require.Len(t, r.PlayerOrder, 3)
	}
}

func TestReadyAndLeaveCommitTogether(t *testing.T) {
	for range 100 {
		rm, r, clients := fullWaitingRoom(t)
		require.NoError(t, rm.SetPlayerReady(clients[0], true))
		require.NoError(t, rm.SetPlayerReady(clients[1], true))
		var wg sync.WaitGroup
		var leaveErr, readyErr error
		wg.Go(func() { leaveErr = rm.LeaveRoom(clients[0]) })
		wg.Go(func() { readyErr = rm.SetPlayerReady(clients[2], true) })
		wg.Wait()
		require.NoError(t, readyErr)
		if leaveErr == nil {
			require.Equal(t, RoomStateWaiting, r.State)
			require.Len(t, r.Players, 2)
		} else {
			require.ErrorIs(t, leaveErr, apperrors.ErrGameStarted)
			require.Equal(t, RoomStateReady, r.State)
			require.Len(t, r.Players, 3)
		}
	}
}

func TestRejoiningFillsVacatedSeatAndPreservesGameOrder(t *testing.T) {
	for _, vacancy := range []int{0, 1, 2} {
		rm, r, clients := fullWaitingRoom(t)
		require.NoError(t, rm.LeaveRoom(clients[vacancy]))
		replacement := testutil.NewSimpleClient("d", "D")
		_, err := rm.JoinRoom(replacement, r.Code)
		require.NoError(t, err)
		require.Equal(t, vacancy, r.Players[replacement.ID].Seat)
		for seat, id := range r.PlayerOrder {
			require.Equal(t, seat, r.Players[id].Seat)
		}
	}
}

func TestMatchedRoomIsCommittedBeforeItIsReturned(t *testing.T) {
	rm := NewRoomManager(nil, config.GameConfig{})
	clients := []types.ClientInterface{testutil.NewSimpleClient("a", "A"), testutil.NewSimpleClient("b", "B"), testutil.NewSimpleClient("c", "C")}
	r, err := rm.CreateMatchedRoom(clients)
	require.NoError(t, err)
	require.Equal(t, RoomStateReady, r.State)
	require.ErrorIs(t, rm.LeaveRoom(clients[0]), apperrors.ErrGameStarted)
	for _, client := range clients {
		messages := client.(*testutil.SimpleClient).SentMessages()
		require.Equal(t, protocol.MsgRoomJoined, messages[0].Type)
		require.Equal(t, protocol.MsgGameStart, messages[1].Type)
	}
}

func TestMatchedRoomFailureDoesNotMoveAnyPlayer(t *testing.T) {
	rm, r, clients := fullWaitingRoom(t)
	free := testutil.NewSimpleClient("d", "D")
	_, err := rm.CreateMatchedRoom([]types.ClientInterface{free, clients[0], clients[1]})
	require.ErrorIs(t, err, apperrors.ErrAlreadyInRoom)
	require.Empty(t, free.GetRoom())
	require.Equal(t, r.Code, clients[0].GetRoom())
	require.Len(t, rm.rooms, 1)
}

func TestCleanupEndedRoomRetainsResultThenReleasesIt(t *testing.T) {
	rm, r, clients := fullWaitingRoom(t)
	r.CreatedAt = time.Now().Add(-24 * time.Hour)
	r.SetState(RoomStateEnded)
	removed := 0
	rm.SetOnRoomRemoved(func(old *Room) {
		require.Nil(t, rm.GetRoom(old.Code)) // callback must run without manager/room locks
		old.SetState(RoomStateEnded)
		removed++
	})
	rm.cleanup()
	require.Same(t, r, rm.GetRoom(r.Code), "retention starts at end, not creation")
	// A result recipient can already be playing in a new room when cleanup runs.
	clients[0].SetRoom("")
	next, err := rm.CreateRoom(clients[0])
	require.NoError(t, err)
	r.EndedAt = time.Now().Add(-endedRoomRetention - time.Second)
	rm.cleanup()
	require.Nil(t, rm.GetRoom(r.Code))
	require.Equal(t, 1, removed)
	require.Equal(t, next.Code, clients[0].GetRoom())
	require.Same(t, next, rm.GetRoom(next.Code))
	rm.cleanup()
	require.Equal(t, 1, removed)
}

type testBotClient struct {
	*testutil.SimpleClient
}

func (b *testBotClient) IsBot() bool { return true }

func TestOwnerSuccessionResetsNewOwnerReadyAndBroadcastsOwner(t *testing.T) {
	rm, r, clients := fullWaitingRoom(t)
	require.Equal(t, "a", r.GetOwnerID())
	require.Equal(t, "a", r.BuildRoomCreatedPayload("a").OwnerID)
	require.Equal(t, "a", r.BuildRoomJoinedPayload("b").OwnerID)

	require.NoError(t, rm.SetPlayerReady(clients[1], true))
	require.True(t, r.Players["b"].Ready)

	require.NoError(t, rm.LeaveRoom(clients[0]))
	require.Equal(t, "b", r.GetOwnerID())
	require.False(t, r.Players["b"].Ready)

	msgs := clients[1].SentMessages()
	var sawLeft, sawReadyReset bool
	for _, m := range msgs {
		if m.Type == protocol.MsgPlayerLeft {
			sawLeft = true
		}
		if m.Type == protocol.MsgPlayerReady {
			sawReadyReset = true
		}
	}
	require.True(t, sawLeft)
	require.True(t, sawReadyReset)
}

func TestStartRoomValidationsAndBotFill(t *testing.T) {
	newBotFactory := func() func() types.ClientInterface {
		seq := 0
		return func() types.ClientInterface {
			seq++
			id := "bot-" + string(rune('0'+seq))
			return &testBotClient{SimpleClient: testutil.NewSimpleClient(id, "🤖Bot")}
		}
	}

	t.Run("rejects non-owner, unready guest, and offline guest", func(t *testing.T) {
		rm, r, clients := fullWaitingRoom(t)
		rm.SetBotFactory(newBotFactory())

		require.ErrorIs(t, rm.StartRoom(clients[1]), apperrors.ErrNotRoomOwner)
		require.ErrorIs(t, rm.StartRoom(clients[0]), apperrors.ErrPlayersNotReady)

		require.NoError(t, rm.SetPlayerReady(clients[1], true))
		require.NoError(t, rm.SetPlayerReady(clients[2], true))
		// Note: clients[0] (owner) never called SetPlayerReady, so checkAllReady stayed false
		require.Equal(t, RoomStateWaiting, r.State)

		r.Players["b"].Offline = true
		require.ErrorIs(t, rm.StartRoom(clients[0]), apperrors.ErrPlayerOffline)
	})

	t.Run("solo owner fills two bots and starts game", func(t *testing.T) {
		rm := NewRoomManager(nil, config.GameConfig{RoomTimeout: 600})
		rm.SetBotFactory(newBotFactory())
		started := 0
		rm.SetOnGameStart(func(*Room) { started++ })

		host := testutil.NewSimpleClient("host", "Host")
		r, err := rm.CreateRoom(host)
		require.NoError(t, err)
		require.NoError(t, rm.StartRoom(host))

		require.Equal(t, 1, started)
		require.Equal(t, RoomStateReady, r.State)
		require.Len(t, r.Players, 3)
		require.Equal(t, []string{"host", "bot-1", "bot-2"}, r.PlayerOrder)
		for seat, id := range r.PlayerOrder {
			require.Equal(t, seat, r.Players[id].Seat)
		}
	})

	t.Run("two humans with vacant middle seat fills bot into middle seat", func(t *testing.T) {
		rm, r, clients := fullWaitingRoom(t)
		rm.SetBotFactory(newBotFactory())
		require.NoError(t, rm.LeaveRoom(clients[1])) // vacate seat 1
		require.NoError(t, rm.SetPlayerReady(clients[2], true))

		require.NoError(t, rm.StartRoom(clients[0]))
		require.Equal(t, RoomStateReady, r.State)
		require.Equal(t, []string{"a", "bot-1", "c"}, r.PlayerOrder)
		require.Equal(t, 1, r.Players["bot-1"].Seat)
	})

	t.Run("three humans starts without adding bots", func(t *testing.T) {
		rm, r, clients := fullWaitingRoom(t)
		rm.SetBotFactory(newBotFactory())
		require.NoError(t, rm.SetPlayerReady(clients[1], true))
		require.NoError(t, rm.SetPlayerReady(clients[2], true))

		require.NoError(t, rm.StartRoom(clients[0]))
		require.Equal(t, RoomStateReady, r.State)
		require.Equal(t, []string{"a", "b", "c"}, r.PlayerOrder)
	})
}
