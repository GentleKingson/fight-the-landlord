package handler

import (
	"log"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/match"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/observability"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// HandlerDeps 处理器依赖
type HandlerDeps struct {
	Server         types.ServerInterface
	RoomManager    *room.RoomManager
	Matcher        *match.Matcher
	ChatLimiter    types.ChatLimiter
	Leaderboard    *storage.LeaderboardManager
	SessionManager *session.SessionManager
	Metrics        *observability.Metrics
	Logger         *slog.Logger
}

// Handler 消息处理器
type Handler struct {
	server         types.ServerInterface
	roomManager    *room.RoomManager
	matcher        *match.Matcher
	chatLimiter    types.ChatLimiter
	leaderboard    *storage.LeaderboardManager
	sessionManager *session.SessionManager
	metrics        *observability.Metrics
	logger         *slog.Logger
	handlers       map[protocol.MessageType]handlerFunc
	games          map[string]gameRegistration
	gamesMu        sync.RWMutex
	// gamesLifecycleMu orders exact registration/removal against final
	// Reconnected and RoomLeft enqueueing. Never call Retire or Matcher while
	// holding it.
	gamesLifecycleMu    sync.Mutex
	legacyChatMessages  atomic.Int64
	chatMessageSequence atomic.Uint64
}

// LegacyChatMessages reports migration traffic that still embeds JSON Chat
// payloads inside the protobuf envelope.
func (h *Handler) LegacyChatMessages() int64 {
	return h.legacyChatMessages.Load()
}

type gameRegistration struct {
	room    *room.Room
	session *session.GameSession
}

// handlerFunc 统一的处理器函数签名
type handlerFunc func(client types.ClientInterface, msg *protocol.Message)

// NewHandler 创建处理器
func NewHandler(deps HandlerDeps) *Handler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	h := &Handler{
		server:         deps.Server,
		roomManager:    deps.RoomManager,
		matcher:        deps.Matcher,
		chatLimiter:    deps.ChatLimiter,
		leaderboard:    deps.Leaderboard,
		sessionManager: deps.SessionManager,
		metrics:        deps.Metrics,
		logger:         logger.With("component", "command"),
		games:          make(map[string]gameRegistration),
	}
	h.initHandlers()
	if h.roomManager != nil {
		h.roomManager.SetOnRoomRemoved(h.handleRoomRemoved)
		h.roomManager.SetOnPresenceChanged(h.handlePresenceChanged)
	}
	return h
}

// GetGameSession 获取房间的游戏会话
func (h *Handler) GetGameSession(roomCode string) *session.GameSession {
	h.gamesMu.RLock()
	defer h.gamesMu.RUnlock()
	return h.games[roomCode].session
}

// SetGameSession publishes a session only while RoomManager still owns the
// exact Room used to construct it. gamesMu serializes this validation with the
// removal callback, closing both deletion-before-registration and code-reuse
// windows.
func (h *Handler) SetGameSession(roomCode string, gs *session.GameSession) bool {
	if gs == nil || h.roomManager == nil {
		return false
	}
	gameRoom := gs.RoomIdentity()
	var previous *session.GameSession
	accepted := gameRoom != nil && gameRoom.Code == roomCode && h.roomManager.WithCurrentRoomPublication(gameRoom, func() {
		h.gamesLifecycleMu.Lock()
		h.gamesMu.Lock()
		// Keep registration serialized with GetGameSession. Disconnect and
		// reconnect update the room first, then look up the session, so either
		// this snapshot observes the event or that lookup updates the published
		// session after this lock is released.
		gs.SetRoomManager(h.roomManager)
		gs.SyncRoomPresence(gameRoom.SnapshotPlayers())
		if current := h.games[roomCode]; current.session != nil && current.session != gs {
			previous = current.session
		}
		h.games[roomCode] = gameRegistration{room: gameRoom, session: gs}
		h.gamesMu.Unlock()
		h.gamesLifecycleMu.Unlock()
	})

	// Retire can wait for an in-progress GameSession action and must never run
	// under gamesMu, which also protects reconnect and room-removal progress.
	if !accepted {
		gs.Retire()
	} else if previous != nil {
		previous.Retire()
	}
	return accepted
}

func (h *Handler) handlePresenceChanged(gameRoom *room.Room, playerID string, online bool) {
	if gameRoom == nil || playerID == "" {
		return
	}
	h.gamesMu.RLock()
	registration := h.games[gameRoom.Code]
	h.gamesMu.RUnlock()
	if registration.room != gameRoom || registration.session == nil || registration.session.RoomIdentity() != gameRoom {
		return
	}
	if online {
		registration.session.PlayerOnline(playerID)
		return
	}
	registration.session.PlayerOffline(playerID)
}

func (h *Handler) handleRoomRemoved(removal room.RoomRemoval) {
	var retired *session.GameSession
	h.gamesLifecycleMu.Lock()
	h.gamesMu.Lock()
	if current := h.games[removal.Code]; current.room == removal.Room {
		delete(h.games, removal.Code)
		retired = current.session
	}
	h.gamesMu.Unlock()
	h.gamesLifecycleMu.Unlock()

	if retired != nil {
		retired.Retire()
	}
	if h.matcher != nil {
		h.matcher.RoomRemoved(removal.Room)
	}
	if removal.Reason == room.RoomRemovalLeft {
		return
	}

	// Serialize the terminal room event with any final reconnect response. If a
	// reconnect won the boundary, RoomLeft is enqueued after it; otherwise the
	// reconnect observes that RoomManager no longer owns the room.
	h.gamesLifecycleMu.Lock()
	message := codec.MustNewMessage(protocol.MsgRoomLeft, protocol.RoomLeftPayload{RoomCode: removal.Code})
	for _, player := range removal.Players {
		if player.Client == nil || player.IsBot {
			continue
		}
		if _, err := types.SendMessageIfIdentity(player.Client, player.ID, "", message); err != nil {
			log.Printf("发送房间 %s 移除事件给玩家 %s 失败: %v", removal.Code, player.ID, err)
		}
	}
	h.gamesLifecycleMu.Unlock()
}

// initHandlers 初始化消息处理器映射
func (h *Handler) initHandlers() {
	h.handlers = map[protocol.MessageType]handlerFunc{
		// 连接操作
		protocol.MsgPing:      h.handlePing,
		protocol.MsgReconnect: h.handleReconnect,

		// 房间操作
		protocol.MsgCreateRoom:    func(c types.ClientInterface, _ *protocol.Message) { h.handleCreateRoom(c) },
		protocol.MsgJoinRoom:      h.handleJoinRoom,
		protocol.MsgLeaveRoom:     func(c types.ClientInterface, _ *protocol.Message) { h.handleLeaveRoom(c) },
		protocol.MsgQuickMatch:    func(c types.ClientInterface, _ *protocol.Message) { h.handleQuickMatch(c) },
		protocol.MsgPracticeMatch: func(c types.ClientInterface, _ *protocol.Message) { h.handlePracticeMatch(c) },
		protocol.MsgCancelMatch:   func(c types.ClientInterface, _ *protocol.Message) { h.handleCancelMatch(c) },
		protocol.MsgReady:         func(c types.ClientInterface, _ *protocol.Message) { h.handleReady(c, true) },
		protocol.MsgCancelReady:   func(c types.ClientInterface, _ *protocol.Message) { h.handleReady(c, false) },

		// 游戏操作
		protocol.MsgBid:       h.handleBid,
		protocol.MsgPlayCards: h.handlePlayCards,
		protocol.MsgPass:      func(c types.ClientInterface, msg *protocol.Message) { h.handlePass(c, msg) },

		// 信息查询
		protocol.MsgGetStats:             func(c types.ClientInterface, _ *protocol.Message) { h.handleGetStats(c) },
		protocol.MsgGetLeaderboard:       h.handleGetLeaderboard,
		protocol.MsgGetRoomList:          func(c types.ClientInterface, _ *protocol.Message) { h.handleGetRoomList(c) },
		protocol.MsgGetOnlineCount:       func(c types.ClientInterface, _ *protocol.Message) { h.handleGetOnlineCount(c) },
		protocol.MsgGetMaintenanceStatus: func(c types.ClientInterface, _ *protocol.Message) { h.handleGetMaintenanceStatus(c) },
		protocol.MsgChat:                 h.handleChat,
	}
}

// Handle 处理消息
func (h *Handler) Handle(client types.ClientInterface, msg *protocol.Message) {
	started := time.Now()
	requestID := ""
	if msg.Command != nil {
		requestID = msg.Command.RequestID
	}
	if handler, ok := h.handlers[msg.Type]; ok {
		handler(client, msg)
		h.logCommandDispatch(client, msg.Type, requestID, "completed", 0, time.Since(started))
		return
	}

	log.Printf("⚠️  未知消息类型: '%s' (来自玩家: %s, ID: %s)", msg.Type, client.GetName(), client.GetID())
	log.Printf("    消息详情: Payload长度=%d bytes", len(msg.Payload))
	sendMessage(client, codec.NewCommandErrorMessage(protocol.ErrCodeInvalidMsg, msg.Type))
	h.logCommandDispatch(client, msg.Type, requestID, "rejected", protocol.ErrCodeInvalidMsg, time.Since(started))
}

func (h *Handler) logCommandDispatch(
	client types.ClientInterface,
	messageType protocol.MessageType,
	requestID, result string,
	errorCode int,
	duration time.Duration,
) {
	logger := h.logger
	if logger == nil {
		logger = slog.Default()
	}
	logEvent := logger.Info
	message := "command dispatch completed"
	if errorCode != 0 {
		logEvent = logger.Warn
		message = "command dispatch rejected"
	}
	logEvent(message,
		"event", "command_dispatch",
		"request_id", requestID,
		"player_id", client.GetID(),
		"client_kind", commandClientKind(client),
		"type", string(messageType),
		"result", result,
		"duration_ms", duration.Milliseconds(),
		"error_code", errorCode,
	)
}

func commandClientKind(client types.ClientInterface) string {
	if client.IsBot() {
		return protocol.ClientKindBot
	}
	if browser, ok := client.(types.WebSessionClient); ok && browser.IsBrowserTransport() {
		return protocol.ClientKindWeb
	}
	return protocol.ClientKindTUI
}

func sendMessage(client types.ClientInterface, message *protocol.Message) {
	if err := types.SendCommandResult(client, message); err != nil {
		log.Printf("发送消息 %s 给玩家 %s 失败: %v", message.Type, client.GetID(), err)
	}
}
