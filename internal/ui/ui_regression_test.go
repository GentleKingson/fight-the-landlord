package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/ui/model"
)

func TestWaitingStartGameAndEscCancelMatch(t *testing.T) {
	upgrader := websocket.Upgrader{}
	received := make(chan protocol.MessageType, 16)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		for {
			_, raw, err := ws.ReadMessage()
			if err != nil {
				return
			}
			msg, err := codec.Decode(raw)
			if err == nil {
				received <- msg.Type
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	m := NewOnlineModel(wsURL)
	defer m.Client().Close()

	require.NoError(t, m.Client().Connect())
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.SetPlayerInfo("host-1", "Host")

	// 1. ESC during PhaseMatching must send MsgCancelMatch and return to Lobby
	m.SetPhase(model.PhaseMatching)
	handled, _ := HandleKeyPress(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.True(t, handled)
	assert.Equal(t, model.PhaseLobby, m.Phase())
	require.Equal(t, protocol.MsgCancelMatch, waitMsgType(t, received))

	// 2. RoomCreated tracks OwnerID, renders owner start hint, and typing S + Enter sends MsgStartGame
	created := codec.MustNewMessage(protocol.MsgRoomCreated, protocol.RoomCreatedPayload{
		RoomCode: "123456",
		OwnerID:  "host-1",
		Player:   protocol.PlayerInfo{ID: "host-1", Name: "Host"},
	})
	m.Update(model.ServerMessage{Msg: created})
	assert.Equal(t, model.PhaseWaiting, m.Phase())
	assert.Equal(t, "host-1", m.Game().State().OwnerID)
	viewStr := m.View().Content
	assert.Contains(t, viewStr, "房主")
	assert.Contains(t, viewStr, "开始游戏")

	m.Input().SetValue("s")
	_, _ = HandleKeyPress(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, protocol.MsgStartGame, waitMsgType(t, received))
}

func waitMsgType(t *testing.T, ch <-chan protocol.MessageType) protocol.MessageType {
	t.Helper()
	select {
	case mt := <-ch:
		return mt
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for client message")
		return ""
	}
}
