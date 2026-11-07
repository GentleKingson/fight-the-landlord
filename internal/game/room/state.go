package room

import "time"

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
	if state == RoomStateEnded && r.State != RoomStateEnded {
		r.EndedAt = time.Now()
	}
	r.State = state
}

func (r *Room) GetState() RoomState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.State
}
