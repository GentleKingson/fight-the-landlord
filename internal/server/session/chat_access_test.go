package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestGameSession_CurrentGameContext(t *testing.T) {
	client := testutil.NewSimpleClient("p1", "Player1")
	gameRoom := room.NewMockRoom("ROOM-A", client)
	gameRoom.SetPlayerOrderForTest([]string{client.GetID()})
	game := NewGameSession(gameRoom, nil, config.GameConfig{})

	gameID, state, member := game.CurrentGameContext(client.GetID())
	assert.Empty(t, gameID)
	assert.Equal(t, GameStateInit, state)
	assert.True(t, member)

	game.mu.Lock()
	game.gameID = "game-1"
	game.state = GameStateEnded
	game.mu.Unlock()

	gameID, state, member = game.CurrentGameContext(client.GetID())
	require.True(t, member)
	assert.Equal(t, "game-1", gameID)
	assert.Equal(t, GameStateEnded, state)

	gameID, state, member = game.CurrentGameContext("outsider")
	assert.False(t, member)
	assert.Equal(t, "game-1", gameID)
	assert.Equal(t, GameStateEnded, state)
}
