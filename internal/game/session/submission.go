package session

// The receipt is part of the same Redis candidate as the action. Reconnect can
// distinguish a lost acknowledgement from an action that never committed.
func (gs *GameSession) submitAction(playerID, token string, action func() error) error {
	return gs.transition(func() error {
		gs.submittingToken = token
		defer func() { gs.submittingToken = "" }()
		if err := action(); err != nil {
			return err
		}
		if gs.actionReceipts == nil {
			gs.actionReceipts = make(map[string]string)
		}
		gs.actionReceipts[playerID] = token
		return nil
	})
}
