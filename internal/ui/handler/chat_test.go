package handler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

func TestFormatChatLineEscapesTerminalControls(t *testing.T) {
	t.Parallel()

	line := formatChatLine(protocol.ChatPayload{
		SenderName: "player\x1b[31m",
		Content:    "hello\n\u202Eworld",
		Time:       time.Date(2026, time.July, 15, 14, 0, 0, 0, time.Local).Unix(),
	})

	assert.NotContains(t, line, "\x1b")
	assert.NotContains(t, line, "\n")
	assert.NotContains(t, line, "\u202E")
	assert.Contains(t, line, `player\u{1B}[31m`)
	assert.Contains(t, line, `hello\u{A}\u{202E}world`)
}
