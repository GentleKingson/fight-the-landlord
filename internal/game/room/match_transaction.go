package room

import (
	"errors"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

var (
	// ErrMatchRoomTransactionEnded indicates that the transaction was already
	// rolled back or is no longer owned by its RoomManager.
	ErrMatchRoomTransactionEnded = errors.New("match room transaction ended")
	// ErrMatchRoomParticipantCount indicates that commit was attempted without
	// exactly three participants.
	ErrMatchRoomParticipantCount = errors.New("match room requires exactly three participants")
	// ErrMatchRoomRosterChanged indicates that a participant changed rooms or
	// that the room membership no longer matches the committed match roster.
	ErrMatchRoomRosterChanged = errors.New("match room roster changed")
)

type matchRoomTransactionState uint8

const (
	matchRoomPending matchRoomTransactionState = iota
	matchRoomPublished
	matchRoomEnded
)

// MatchRoomTransaction builds a match room without exposing partial
// membership. Its state and room membership are protected by RoomManager.mu
// followed by Room.mu, matching the rest of RoomManager's lock order.
type MatchRoomTransaction struct {
	manager      *RoomManager
	room         *Room
	participants []types.ClientInterface
	state        matchRoomTransactionState
}

// Room returns the immutable room identity reserved by this transaction. The
// room remains unpublished until Commit succeeds; exposing its identity lets
// the matcher bind cancellation before the Commit/removal window opens.
func (tx *MatchRoomTransaction) Room() *Room {
	if tx == nil {
		return nil
	}
	return tx.room
}

// BeginMatchRoom reserves an unpublished room and adds the first participant.
// It deliberately does not bind the client, persist the room, or send messages.
func (rm *RoomManager) BeginMatchRoom(first types.ClientInterface) (*MatchRoomTransaction, error) {
	if first == nil || first.GetRoom() != "" {
		return nil, ErrMatchRoomRosterChanged
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.closed {
		return nil, ErrRoomManagerClosed
	}
	if rm.pendingRooms == nil {
		rm.pendingRooms = make(map[string]*MatchRoomTransaction)
	}

	code := rm.generateRoomCode()
	matchRoom := newRoom(code, time.Now())
	firstPlayer := newRoomPlayer(first, 0)
	matchRoom.players[firstPlayer.ID] = firstPlayer
	matchRoom.playerOrder = append(matchRoom.playerOrder, firstPlayer.ID)
	tx := &MatchRoomTransaction{
		manager:      rm,
		room:         matchRoom,
		participants: []types.ClientInterface{first},
		state:        matchRoomPending,
	}
	rm.pendingRooms[code] = tx
	return tx, nil
}

// Join stages one more participant without making the room externally visible.
func (tx *MatchRoomTransaction) Join(client types.ClientInterface) error {
	if tx == nil || tx.manager == nil || client == nil {
		return ErrMatchRoomTransactionEnded
	}
	if client.GetRoom() != "" {
		return ErrMatchRoomRosterChanged
	}

	rm := tx.manager
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.closed {
		return ErrMatchRoomTransactionEnded
	}
	if tx.state != matchRoomPending || rm.pendingRooms[tx.room.Code] != tx {
		return ErrMatchRoomTransactionEnded
	}

	tx.room.mu.Lock()
	defer tx.room.mu.Unlock()
	if len(tx.room.players) >= 3 {
		return apperrors.ErrRoomFull
	}
	playerID := client.GetID()
	if _, duplicate := tx.room.players[playerID]; duplicate {
		return ErrMatchRoomRosterChanged
	}
	player := newRoomPlayer(client, tx.room.nextAvailableSeatLocked())
	tx.room.players[player.ID] = player
	tx.room.insertPlayerOrderLocked(player.ID)
	tx.participants = append(tx.participants, client)
	return nil
}

// Commit atomically publishes a complete three-player room and binds all three
// client identities while RoomManager and Room ownership are held. Delivery and
// persistence remain the caller's responsibility after Commit returns.
func (tx *MatchRoomTransaction) Commit() (*Room, error) {
	if tx == nil || tx.manager == nil {
		return nil, ErrMatchRoomTransactionEnded
	}

	rm := tx.manager
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.closed {
		return nil, ErrMatchRoomTransactionEnded
	}
	if tx.state != matchRoomPending || rm.pendingRooms[tx.room.Code] != tx {
		return nil, ErrMatchRoomTransactionEnded
	}

	tx.room.publishMu.Lock()
	defer tx.room.publishMu.Unlock()
	tx.room.mu.Lock()
	defer tx.room.mu.Unlock()
	if len(tx.participants) != 3 || len(tx.room.players) != 3 {
		return nil, ErrMatchRoomParticipantCount
	}
	for index, client := range tx.participants {
		playerID := tx.room.playerOrder[index]
		player, exists := tx.room.players[playerID]
		if !exists || player == nil || player.Client != client {
			return nil, ErrMatchRoomRosterChanged
		}
	}
	bound := make([]PlayerSnapshot, 0, len(tx.participants))
	for index, client := range tx.participants {
		player := tx.room.players[tx.room.playerOrder[index]]
		if !types.CompareAndSetRoom(client, player.ID, "", tx.room.Code) {
			for _, current := range bound {
				types.CompareAndSetRoom(current.Client, current.ID, tx.room.Code, "")
			}
			return nil, ErrMatchRoomRosterChanged
		}
		bound = append(bound, snapshotPlayer(player))
	}

	delete(rm.pendingRooms, tx.room.Code)
	rm.rooms[tx.room.Code] = tx.room
	tx.state = matchRoomPublished
	return tx.room, nil
}

// Rollback retires the exact room owned by this transaction. It is idempotent
// and clears only current member handles still bound to this room, avoiding
// mutation of stale connections that may share a SessionManager identity.
func (tx *MatchRoomTransaction) Rollback() {
	if tx == nil || tx.manager == nil {
		return
	}

	rm := tx.manager
	rm.mu.Lock()
	if tx.state == matchRoomEnded {
		rm.mu.Unlock()
		return
	}

	tx.room.publishMu.Lock()
	tx.room.mu.Lock()
	wasPublished := tx.state == matchRoomPublished
	if rm.pendingRooms[tx.room.Code] == tx {
		delete(rm.pendingRooms, tx.room.Code)
	}
	var removal roomRemovalDispatch
	removed := false
	if wasPublished {
		removal, removed = rm.removePublishedRoomLocked(tx.room, RoomRemovalRollback)
	}
	if !removed {
		tx.room.state = RoomStateEnded
	}
	tx.state = matchRoomEnded
	tx.room.mu.Unlock()
	rm.mu.Unlock()
	tx.room.publishMu.Unlock()

	if removed {
		rm.dispatchRoomRemoval(removal)
	}
}

// ReadyAllAndStart validates that expected is the exact current three-client
// roster, then readies it and transitions the room atomically. No delivery is
// performed while the room lock is held.
func (r *Room) ReadyAllAndStart(expected []types.ClientInterface) ([]PlayerSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != RoomStateWaiting {
		return nil, apperrors.ErrGameStarted
	}
	if len(expected) != 3 || len(r.players) != 3 || len(r.playerOrder) != 3 {
		return nil, ErrMatchRoomParticipantCount
	}
	for index, client := range expected {
		if client == nil {
			return nil, ErrMatchRoomRosterChanged
		}
		playerID := client.GetID()
		player, exists := r.players[playerID]
		if client.GetRoom() != r.Code || !exists || player == nil || player.Client != client || r.playerOrder[index] != playerID {
			return nil, ErrMatchRoomRosterChanged
		}
	}
	for _, player := range r.players {
		player.Ready = true
	}
	r.state = RoomStateReady
	return r.snapshotPlayersLocked(), nil
}
