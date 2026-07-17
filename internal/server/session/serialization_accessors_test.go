package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSerializationAccessorsReturnDefensiveState(t *testing.T) {
	game, sessions := newSnapshotTestSession(t)
	t.Cleanup(func() { require.NoError(t, sessions.Close()) })

	game.SetMetrics(nil)
	assert.Equal(t, game.room, game.RoomIdentity())
	assert.Nil(t, (*GameSession)(nil).RoomIdentity())
	assert.Nil(t, game.CurrentEventMeta())

	game.Start()
	require.NotNil(t, game.CurrentEventMeta())
	assert.Equal(t, game.currentPlayer, game.GetCurrentPlayerForSerialization())
	assert.Equal(t, game.currentBidder, game.GetCurrentBidderForSerialization())
	assert.Equal(t, game.landlordCandidate, game.GetHighestBidderForSerialization())
	players := game.GetPlayersForSerialization()
	require.Len(t, players, len(game.players))
	require.NotSame(t, game.players[0], players[0])
	players[0].Hand = nil
	assert.NotEmpty(t, game.players[0].Hand)
	assert.Equal(t, game.bottomCards, game.GetBottomCardsForSerialization())
}
