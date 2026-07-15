package room

import (
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// IsCurrentClient verifies that expected is the delivery handle currently
// authorized for its player identity.
func (r *Room) IsCurrentClient(expected types.ClientInterface) bool {
	if expected == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.isCurrentMemberLocked(expected.GetID(), expected)
}

// IsCurrentMember verifies an immutable logical player identity and physical
// delivery handle together.
func (r *Room) IsCurrentMember(playerID string, expected types.ClientInterface) bool {
	if playerID == "" || expected == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.isCurrentMemberLocked(playerID, expected)
}

func (r *Room) isCurrentMemberLocked(playerID string, expected types.ClientInterface) bool {
	player, exists := r.players[playerID]
	return exists && player != nil && player.Client == expected
}

// BroadcastFromMember verifies the sender and snapshots recipients under the
// room lock, then performs network writes without holding that lock.
func (r *Room) BroadcastFromMember(sender types.ClientInterface, msg *protocol.Message) bool {
	if sender == nil {
		return false
	}
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	r.mu.RLock()
	member, exists := r.players[sender.GetID()]
	if !exists || member == nil || member.Client != sender {
		r.mu.RUnlock()
		return false
	}
	recipients := r.snapshotDeliveryRecipientsLocked("")
	r.mu.RUnlock()

	sendToRoomRecipients(r.Code, recipients, msg)
	return true
}
