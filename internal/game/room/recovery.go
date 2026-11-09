package room

import "fmt"

// RestoreRoom runs before serving connections; duplicate codes fail closed.
func (rm *RoomManager) RestoreRoom(r *Room) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.rooms[r.Code] != nil {
		return fmt.Errorf("duplicate recovered room %s", r.Code)
	}
	rm.rooms[r.Code] = r
	return nil
}
