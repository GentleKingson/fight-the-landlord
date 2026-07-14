package session

// CurrentGameContext returns the authoritative game identifier, state, and
// membership under one read lock. The identifier stays available in the ended
// state so result-screen chat remains scoped to the completed deal.
func (gs *GameSession) CurrentGameContext(playerID string) (gameID string, state GameState, member bool) {
	gs.mu.RLock()
	defer gs.mu.RUnlock()

	for _, player := range gs.players {
		if player != nil && player.ID == playerID {
			return gs.gameID, gs.state, true
		}
	}
	return gs.gameID, gs.state, false
}
