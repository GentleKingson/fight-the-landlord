package room

// RoomState 房间状态
type RoomState int

const (
	RoomStateWaiting RoomState = iota
	RoomStateReady
	RoomStateBidding
	RoomStatePlaying
	RoomStateEnded
)

// SetState synchronizes game-session transitions with room cleanup and listing.
func (r *Room) SetState(state RoomState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.State = state
}
