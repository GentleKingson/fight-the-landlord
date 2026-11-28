package session

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

type identityBotClient struct{ *testutil.SimpleClient }

func (*identityBotClient) IsBot() bool { return true }

func TestReconnectPreservesBotIdentity(t *testing.T) {
	cfg := config.GameConfig{BidTimeout: 600, TurnTimeout: 600}
	manager := room.NewRoomManager(nil, cfg)
	human := testutil.NewSimpleClient("bot-human", "机器人")
	r, err := manager.CreateRoom(human)
	require.NoError(t, err)
	_, err = manager.JoinRoom(&identityBotClient{testutil.NewSimpleClient("p2", "Player")}, r.Code)
	require.NoError(t, err)
	_, err = manager.JoinRoom(testutil.NewSimpleClient("p3", "Player"), r.Code)
	require.NoError(t, err)
	gs := NewGameSession(r, nil, cfg)
	gs.Start()
	t.Cleanup(gs.StopAllTimers)
	client := testutil.NewSimpleClient(human.ID, human.Name)
	client.SetRoom(r.Code)
	require.NoError(t, gs.ReconnectClient(manager, client, NewSessionManager(), &protocol.ReconnectedPayload{PlayerID: human.ID}))
	require.NotEmpty(t, client.Messages)
	require.Equal(t, protocol.MsgReconnected, client.Messages[0].Type)
	snapshot, err := codec.ParsePayload[protocol.ReconnectedPayload](client.Messages[0])
	require.NoError(t, err)
	require.NotNil(t, snapshot.GameState)
	require.Len(t, snapshot.GameState.Players, 3)
	for _, player := range snapshot.GameState.Players {
		require.Equal(t, player.ID == "p2", player.IsBot, "identity for %s", player.ID)
		if player.IsBot {
			require.True(t, player.Online)
		}
	}
}
