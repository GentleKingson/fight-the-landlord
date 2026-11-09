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
	RoomStateDealing
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

// SyncRetentionTimes uses the same timestamps as the committed game snapshot.
func (r *Room) SyncRetentionTimes(offlineAt, endedAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.AllOfflineAt, r.EndedAt = offlineAt, endedAt
}

// SetRetentionPolicy preserves an existing game's policy across configuration
// changes on restart. Waiting rooms continue to use the manager's defaults.
func (r *Room) SetRetentionPolicy(offline, result time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.offlineRetention, r.resultRetention = offline, result
}

func (r *Room) offlineRetentionDuration(fallback time.Duration) time.Duration {
	if r.offlineRetention > 0 {
		return r.offlineRetention
	}
	return fallback
}

func (r *Room) resultRetentionDuration(fallback time.Duration) time.Duration {
	if r.resultRetention > 0 {
		return r.resultRetention
	}
	return fallback
}
