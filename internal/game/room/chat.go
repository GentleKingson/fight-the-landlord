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
	player, exists := r.players[expected.GetID()]
	return exists && player != nil && player.Client == expected
}

// BroadcastFromMember verifies the sender and snapshots recipients under the
// room lock, then performs network writes without holding that lock.
func (r *Room) BroadcastFromMember(sender types.ClientInterface, msg *protocol.Message) bool {
	if sender == nil {
		return false
	}
	r.mu.RLock()
	member, exists := r.players[sender.GetID()]
	if !exists || member == nil || member.Client != sender {
		r.mu.RUnlock()
		return false
	}
	recipients := r.snapshotRecipientsLocked("")
	r.mu.RUnlock()

	sendToRecipients(recipients, msg)
	return true
}
