package room

import (
	"fmt"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/apperrors"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// CreateMatchedRoom commits all three players to a game before exposing the room.
// A simultaneous leave/create/join request cannot remove a seat between matching
// and GameSession.Start. No partially assembled room is published on failure.
func (rm *RoomManager) CreateMatchedRoom(clients []types.ClientInterface) (*Room, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if len(clients) != 3 {
		return nil, fmt.Errorf("匹配需要三位玩家")
	}
	seen := make(map[string]bool, 3)
	for _, client := range clients {
		if client.GetRoom() != "" || seen[client.GetID()] {
			return nil, apperrors.ErrAlreadyInRoom
		}
		seen[client.GetID()] = true
	}
	r := &Room{Code: rm.generateRoomCode(), State: RoomStateWaiting, Players: make(map[string]*RoomPlayer), CreatedAt: time.Now()}
	for seat, client := range clients {
		r.Players[client.GetID()] = &RoomPlayer{Client: client, Seat: seat, Ready: true}
		r.PlayerOrder = append(r.PlayerOrder, client.GetID())
		client.SetRoom(r.Code)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, client := range clients {
		client.SendMessage(codec.MustNewMessage(protocol.MsgRoomJoined, protocol.RoomJoinedPayload{
			RoomCode: r.Code, Player: r.GetPlayerInfo(client.GetID()), Players: r.GetAllPlayersInfo(),
		}))
	}
	if err := r.startGameLocked(); err != nil {
		return nil, err
	}
	rm.rooms[r.Code] = r
	return r, nil
}
