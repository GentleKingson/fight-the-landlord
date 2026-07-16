package room

import "github.com/palemoky/fight-the-landlord/internal/types"

type roomRemovalDispatch struct {
	removal  RoomRemoval
	callback func(RoomRemoval)
}

// removePublishedRoomLocked is the only map-removal path for a published
// room. The caller owns RoomManager.mu, gameRoom.publishMu, and gameRoom.mu. It
// performs only state mutation and persistence queueing; callbacks, Bot
// shutdown, and delivery are deliberately left until all three are released.
func (rm *RoomManager) removePublishedRoomLocked(gameRoom *Room, reason RoomRemovalReason) (roomRemovalDispatch, bool) {
	if gameRoom == nil || rm.rooms[gameRoom.Code] != gameRoom {
		return roomRemovalDispatch{}, false
	}

	players := gameRoom.snapshotPlayersLocked()
	for _, player := range players {
		types.CompareAndSetRoom(player.Client, player.ID, gameRoom.Code, "")
	}
	gameRoom.state = RoomStateEnded
	delete(rm.rooms, gameRoom.Code)

	if rm.persistenceReady() {
		if rm.retiringRooms == nil {
			rm.retiringRooms = make(map[string]*Room)
		}
		rm.retiringRooms[gameRoom.Code] = gameRoom
		rm.enqueueRoomDelete(gameRoom.Code, gameRoom)
	}

	return roomRemovalDispatch{
		removal: RoomRemoval{
			Code:    gameRoom.Code,
			Room:    gameRoom,
			Players: players,
			Reason:  reason,
		},
		callback: rm.onRoomRemoved,
	}, true
}

func (rm *RoomManager) dispatchRoomRemoval(dispatch roomRemovalDispatch) {
	// Retire sessions and unlink external lifecycle state before any Close or
	// delivery can block. RoomManager and Room locks were released by callers.
	if dispatch.callback != nil {
		dispatch.callback(dispatch.removal)
	}
	for _, player := range dispatch.removal.Players {
		closeRemovedBot(player.Client)
	}
}

// RemoveRoom retires only the supplied in-memory room identity. A delayed
// owner cannot remove a newer room that happens to use the same code.
func (rm *RoomManager) RemoveRoom(gameRoom *Room, reason RoomRemovalReason) bool {
	if gameRoom == nil {
		return false
	}
	rm.mu.Lock()
	if rm.rooms[gameRoom.Code] != gameRoom {
		rm.mu.Unlock()
		return false
	}
	gameRoom.publishMu.Lock()
	gameRoom.mu.Lock()
	dispatch, removed := rm.removePublishedRoomLocked(gameRoom, reason)
	gameRoom.mu.Unlock()
	rm.mu.Unlock()
	gameRoom.publishMu.Unlock()
	if removed {
		rm.dispatchRoomRemoval(dispatch)
	}
	return removed
}

func closeRemovedBot(client types.ClientInterface) {
	if client != nil && client.IsBot() {
		client.Close()
	}
}

// SetOnRoomRemoved installs the lifecycle callback for published rooms. The
// callback is captured at the removal linearization point and always invoked
// after RoomManager.mu and Room.mu have been released.
func (rm *RoomManager) SetOnRoomRemoved(callback func(RoomRemoval)) {
	rm.mu.Lock()
	rm.onRoomRemoved = callback
	rm.mu.Unlock()
}
