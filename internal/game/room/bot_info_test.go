package room

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

type botInfoClient struct{ *testutil.SimpleClient }

func (*botInfoClient) IsBot() bool { return true }

func TestRoomAndGameStartPreserveBotIdentity(t *testing.T) {
	t.Parallel()
	manager := NewRoomManager(nil, config.GameConfig{})
	// Names and IDs deliberately do not signal whether a client is a bot.
	human := testutil.NewSimpleClient("bot-human", "机器人")
	r, err := manager.CreateRoom(human)
	require.NoError(t, err)
	bot := &botInfoClient{testutil.NewSimpleClient("p2", "Player")}
	_, err = manager.JoinRoom(bot, r.Code)
	require.NoError(t, err)
	_, err = manager.JoinRoom(testutil.NewSimpleClient("p3", "Player"), r.Code)
	require.NoError(t, err)
	require.False(t, r.GetPlayerInfo(human.ID).IsBot)
	require.True(t, r.GetPlayerInfo(bot.ID).IsBot)
	expected := r.GetAllPlayersInfo()
	require.NoError(t, r.StartGame())
	message := human.Messages[len(human.Messages)-1]
	require.Equal(t, protocol.MsgGameStart, message.Type)
	payload, err := codec.ParsePayload[protocol.GameStartPayload](message)
	require.NoError(t, err)
	require.Equal(t, expected, payload.Players)
	require.False(t, payload.Players[0].IsBot)
	require.True(t, payload.Players[1].IsBot)
	require.False(t, payload.Players[2].IsBot)
}
