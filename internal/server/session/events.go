package session

import (
	"time"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

// CurrentEventMeta returns a concurrency-safe snapshot of the current game
// stream watermark. It does not advance the watermark.
func (gs *GameSession) CurrentEventMeta() *protocol.EventMeta {
	gs.mu.RLock()
	defer gs.mu.RUnlock()

	if gs.gameID == "" {
		return nil
	}
	return gs.currentEventMetaLocked(time.Now())
}

// EventMetaFromGameStateDTO preserves the exact watermark and clock values
// used to construct an authoritative reconnect snapshot.
func EventMetaFromGameStateDTO(dto *protocol.GameStateDTO) *protocol.EventMeta {
	if dto == nil || dto.GameID == "" {
		return nil
	}
	return &protocol.EventMeta{
		StreamID:       gameStreamID(dto.GameID),
		EventVersion:   dto.SnapshotVersion,
		GameID:         dto.GameID,
		TurnID:         dto.TurnID,
		ServerTimeMS:   dto.ServerTimeMS,
		TurnDeadlineMS: dto.TurnDeadlineMS,
	}
}

// nextEventMetaLocked advances the authoritative event watermark. Callers
// must hold gs.mu and should call it only after the corresponding mutation has
// committed and any turn deadline has been installed.
func (gs *GameSession) nextEventMetaLocked() *protocol.EventMeta {
	gs.snapshotVersion++
	return gs.currentEventMetaLocked(time.Now())
}

// markStateChangedLocked advances the snapshot watermark for a committed
// mutation that has no GameSession wire event, such as pausing a turn while a
// player is offline.
func (gs *GameSession) markStateChangedLocked() {
	gs.snapshotVersion++
}

func (gs *GameSession) currentEventMetaLocked(now time.Time) *protocol.EventMeta {
	return &protocol.EventMeta{
		StreamID:       gameStreamID(gs.gameID),
		EventVersion:   gs.snapshotVersion,
		GameID:         gs.gameID,
		TurnID:         gs.turnID,
		ServerTimeMS:   now.UnixMilli(),
		TurnDeadlineMS: gs.turnDeadlineMS(),
	}
}

func (gs *GameSession) newGameEventMessage(messageType protocol.MessageType, payload any) *protocol.Message {
	return newGameEventMessageWithMeta(messageType, payload, gs.nextEventMetaLocked())
}

func newGameEventMessageWithMeta(messageType protocol.MessageType, payload any, event *protocol.EventMeta) *protocol.Message {
	message := codec.MustNewMessage(messageType, payload)
	message.Event = event
	return message
}

func gameStreamID(gameID string) string {
	return "game:" + gameID
}
