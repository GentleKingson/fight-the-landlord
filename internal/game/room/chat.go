package room

import (
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// BroadcastFromMember verifies the sender and snapshots recipients under the
// room lock, then performs network writes without holding that lock.
func (r *Room) BroadcastFromMember(senderID string, msg *protocol.Message) bool {
	r.mu.RLock()
	sender, exists := r.Players[senderID]
	if !exists || sender == nil || sender.Client == nil {
		r.mu.RUnlock()
		return false
	}
	recipients := make([]types.ClientInterface, 0, len(r.Players))
	for _, player := range r.Players {
		if player != nil && player.Client != nil {
			recipients = append(recipients, player.Client)
		}
	}
	r.mu.RUnlock()

	for _, client := range recipients {
		client.SendMessage(msg)
	}
	return true
}
