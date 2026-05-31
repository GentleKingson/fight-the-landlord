// Package model contains the UI model implementations.
package model

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/timer"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/sound"
	"github.com/palemoky/fight-the-landlord/internal/transport"
	"github.com/palemoky/fight-the-landlord/internal/ui/common"
)

// OnlineModel is the main model for online game mode.
type OnlineModel struct {
	client *transport.Client
	phase  GamePhase
	error  string

	// Player info
	playerID   string
	playerName string

	matchingStartTime time.Time

	// Network state
	latency int64

	// Reconnect state
	reconnecting      bool
	reconnectAttempt  int
	reconnectMaxTries int
	reconnectChan     chan tea.Msg

	// Maintenance mode
	maintenanceMode bool

	// System notifications
	notifications map[NotificationType]*SystemNotification

	// Sub-models
	lobby *LobbyModel
	game  *GameModel

	// Audio
	soundManager *sound.SoundManager

	// UI components
	input  *textinput.Model
	timer  timer.Model
	width  int
	height int

	// View renderer (injected to break circular import)
	viewRenderer func(Model, GamePhase) string

	// Key handler (injected to break circular import)
	keyHandler func(Model, tea.KeyMsg) (bool, tea.Cmd)

	// Server message handler (injected to break circular import)
	serverMessageHandler func(Model, *protocol.Message) tea.Cmd
}

// NewOnlineModel creates a new OnlineModel.
func NewOnlineModel(serverURL string) *OnlineModel {
	ti := textinput.New()
	ti.Placeholder = "输入选项 (1-7) 或房间号"
	ti.CharLimit = 20
	ti.SetWidth(30)
	ti.Focus()

	c := transport.NewClient(serverURL)
	reconnectChan := make(chan tea.Msg, 10)

	m := &OnlineModel{
		client:            c,
		phase:             PhaseConnecting,
		input:             &ti,
		reconnectMaxTries: 5,
		reconnectChan:     reconnectChan,
		lobby:             NewLobbyModel(c, &ti),
		game:              NewGameModel(c, &ti),
		soundManager:      sound.NewSoundManager(),
		notifications:     make(map[NotificationType]*SystemNotification),
	}

	// Set up reconnect callbacks
	c.OnReconnecting = func(attempt, maxTries int) {
		select {
		case reconnectChan <- ReconnectingMsg{Attempt: attempt, MaxTries: maxTries}:
		default:
		}
	}

	c.OnReconnect = func() {
		select {
		case reconnectChan <- ReconnectSuccessMsg{}:
		default:
		}
	}

	return m
}

func (m *OnlineModel) Init() tea.Cmd {
	go func() {
		_ = m.soundManager.Init()
	}()

	return tea.Batch(
		m.connectToServer(),
		textinput.Blink,
		m.listenForReconnect(),
	)
}

func (m *OnlineModel) listenForReconnect() tea.Cmd {
	return func() tea.Msg {
		msg := <-m.reconnectChan
		return msg
	}
}

func (m *OnlineModel) connectToServer() tea.Cmd {
	return func() tea.Msg {
		if err := m.client.Connect(); err != nil {
			return ConnectionErrorMsg{Err: err}
		}
		return ConnectedMsg{}
	}
}

func (m *OnlineModel) listenForMessages() tea.Cmd {
	return func() tea.Msg {
		msg, err := m.client.Receive()
		if err != nil {
			return ConnectionErrorMsg{Err: err}
		}
		return ServerMessage{Msg: msg}
	}
}

// --- Model interface implementation ---

func (m *OnlineModel) Phase() GamePhase         { return m.phase }
func (m *OnlineModel) SetPhase(phase GamePhase) { m.phase = phase }
func (m *OnlineModel) PlayerID() string         { return m.playerID }
func (m *OnlineModel) PlayerName() string       { return m.playerName }
func (m *OnlineModel) SetPlayerInfo(id, name string) {
	m.playerID = id
	m.playerName = name
}
func (m *OnlineModel) Client() *transport.Client { return m.client }
func (m *OnlineModel) Input() *textinput.Model   { return m.input }
func (m *OnlineModel) Timer() *timer.Model       { return &m.timer }
func (m *OnlineModel) SetTimer(t timer.Model)    { m.timer = t }
func (m *OnlineModel) Lobby() LobbyAccessor      { return m.lobby }
func (m *OnlineModel) Game() GameAccessor        { return m.game }
func (m *OnlineModel) Width() int                { return m.width }
func (m *OnlineModel) Height() int               { return m.height }

func (m *OnlineModel) SetNotification(notifyType NotificationType, message string, temporary bool) {
	m.notifications[notifyType] = &SystemNotification{
		Message:   message,
		Type:      notifyType,
		Temporary: temporary,
	}
}

func (m *OnlineModel) ClearNotification(notifyType NotificationType) {
	delete(m.notifications, notifyType)
}

func (m *OnlineModel) GetCurrentNotification() *SystemNotification {
	priorityOrder := []NotificationType{
		NotifyError,
		NotifyRateLimit,
		NotifyReconnecting,
		NotifyReconnectSuccess,
		NotifyMaintenance,
		NotifyOnlineCount,
	}

	for _, notifyType := range priorityOrder {
		if notification, exists := m.notifications[notifyType]; exists {
			return notification
		}
	}
	return nil
}

func (m *OnlineModel) EnterLobby() {
	m.phase = PhaseLobby
	m.error = ""

	// 大厅播放欢迎背景音乐（循环），覆盖上一局的对局 BGM
	m.soundManager.PlayBGM("bgm_welcome")
	m.input.Reset()
	m.input.Placeholder = "输入选项 (1-7) 或房间号"
	m.input.Focus()

	// 清理游戏状态
	m.game.ClearChatHistory()
	m.game.SetShowQuickMsgMenu(false)
	m.game.SetShowingHelp(false)
}

func (m *OnlineModel) IsMaintenanceMode() bool          { return m.maintenanceMode }
func (m *OnlineModel) SetMaintenanceMode(mode bool)     { m.maintenanceMode = mode }
func (m *OnlineModel) MatchingStartTime() time.Time     { return m.matchingStartTime }
func (m *OnlineModel) SetMatchingStartTime(t time.Time) { m.matchingStartTime = t }
func (m *OnlineModel) PlaySound(name string)            { m.soundManager.Play(name) }
func (m *OnlineModel) PlaySequence(names ...string)     { m.soundManager.PlaySequence(names...) }
func (m *OnlineModel) PlayBGM(name string)              { m.soundManager.PlayBGM(name) }
func (m *OnlineModel) PlayBGMAnyOf(names ...string)     { m.soundManager.PlayBGMAnyOf(names...) }
func (m *OnlineModel) StopBGM()                         { m.soundManager.StopBGM() }
func (m *OnlineModel) ToggleMute() bool                 { return m.soundManager.ToggleMute() }
func (m *OnlineModel) Muted() bool                      { return m.soundManager.Muted() }

// LobbyDirect returns the concrete LobbyModel for internal use.
func (m *OnlineModel) LobbyDirect() *LobbyModel { return m.lobby }

// GameDirect returns the concrete GameModel for internal use.
func (m *OnlineModel) GameDirect() *GameModel { return m.game }

// ReconnectChan returns the reconnect channel.
func (m *OnlineModel) ReconnectChan() chan tea.Msg { return m.reconnectChan }

// Latency returns the current latency.
func (m *OnlineModel) Latency() int64 { return m.latency }

// SetLatency sets the latency.
func (m *OnlineModel) SetLatency(l int64) { m.latency = l }

// Error returns the current error message.
func (m *OnlineModel) Error() string { return m.error }

// SetError sets the error message.
func (m *OnlineModel) SetError(e string) { m.error = e }

// IsReconnecting returns whether the model is reconnecting.
func (m *OnlineModel) IsReconnecting() bool { return m.reconnecting }

// SetReconnecting sets the reconnecting state.
func (m *OnlineModel) SetReconnecting(r bool) { m.reconnecting = r }

// ReconnectAttempt returns the current reconnect attempt.
func (m *OnlineModel) ReconnectAttempt() int { return m.reconnectAttempt }

// SetReconnectAttempt sets the reconnect attempt.
func (m *OnlineModel) SetReconnectAttempt(a int) { m.reconnectAttempt = a }

// ReconnectMaxTries returns the max reconnect tries.
func (m *OnlineModel) ReconnectMaxTries() int { return m.reconnectMaxTries }

// SetReconnectMaxTries sets the max reconnect tries.
func (m *OnlineModel) SetReconnectMaxTries(t int) { m.reconnectMaxTries = t }

// dispatchMessage 分发消息到对应的处理函数，返回命令和是否需要提前返回
func (m *OnlineModel) dispatchMessage(msg tea.Msg) (cmds []tea.Cmd, earlyReturn bool, result tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.handleWindowSize(msg)

	case ConnectedMsg:
		cmds = append(cmds, m.handleConnected())

	case ConnectionErrorMsg:
		m.handleConnectionError(msg)

	case ReconnectingMsg:
		cmds = append(cmds, m.handleReconnecting(msg))

	case ReconnectSuccessMsg:
		cmds = append(cmds, m.handleReconnectSuccess()...)

	case ClearReconnectMsg:
		m.handleClearReconnect()

	case ClearErrorMsg:
		m.error = ""

	case ClearSystemNotificationMsg:
		m.ClearNotification(NotifyError)
		m.ClearNotification(NotifyRateLimit)

	case GameOverDelayMsg:
		m.phase = PhaseGameOver
		m.input.Placeholder = "按回车返回大厅"
		m.input.Focus()

	case ClearInputErrorMsg:
		m.handleClearInputError()

	case ServerMessage:
		cmds = append(cmds, m.processServerMessage(msg)...)

	case tea.KeyMsg:
		if handled, keyCmd := m.processKeyMsg(msg); handled {
			return nil, true, keyCmd
		}

	case timer.TickMsg, timer.TimeoutMsg:
		// 出牌倒计时进入最后 10 秒时播放一次提醒音
		m.checkPlayReminder()
	}

	return cmds, false, nil
}

// checkPlayReminder plays the "出牌提醒" sound once when the local player's
// play countdown enters its final 10 seconds.
func (m *OnlineModel) checkPlayReminder() {
	if m.phase != PhasePlaying || m.game.BellPlayed() {
		return
	}
	if m.game.State().CurrentTurn != m.playerID {
		return
	}
	start := m.game.TimerStartTime()
	if start.IsZero() {
		return
	}
	if m.game.TimerDuration()-time.Since(start) <= 10*time.Second {
		m.PlaySound("turn")
		m.game.SetBellPlayed(true)
	}
}

// Update handles tea messages.
func (m *OnlineModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	// Dispatch message
	dispatchCmds, earlyReturn, result := m.dispatchMessage(msg)
	if earlyReturn {
		return m, result
	}
	cmds = append(cmds, dispatchCmds...)

	// Update timer
	m.timer, cmd = m.timer.Update(msg)
	cmds = append(cmds, cmd)

	// Update input
	newInput, cmd := m.input.Update(msg)
	*m.input = newInput
	cmds = append(cmds, cmd)

	// Matching phase tick
	if m.phase == PhaseMatching {
		cmds = append(cmds, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return tea.WindowSizeMsg{Width: m.width, Height: m.height}
		}))
	}

	return m, tea.Batch(cmds...)
}

// View renders the model.
func (m *OnlineModel) View() tea.View {
	if m.width == 0 {
		return tea.NewView("Loading...")
	}

	var content string

	switch m.phase {
	case PhaseConnecting:
		content = m.connectingView()
	case PhaseMatching:
		content = m.matchingView()
	default:
		// Use injected viewRenderer for phases that require view package
		if m.viewRenderer != nil {
			content = m.viewRenderer(m, m.phase)
		} else {
			content = "View renderer not initialized"
		}
	}

	// 播放声音（未静音）时在终端标签标题加上喇叭 emoji
	title := "欢乐斗地主"
	if !m.Muted() {
		title = "🔊 " + title
	}

	return tea.View{
		Content:     common.DocStyle.Render(content),
		AltScreen:   true,
		WindowTitle: title,
	}
}

// SetViewRenderer sets the view rendering function.
func (m *OnlineModel) SetViewRenderer(fn func(Model, GamePhase) string) {
	m.viewRenderer = fn
}

// SetKeyHandler sets the keyboard event handler function.
func (m *OnlineModel) SetKeyHandler(fn func(Model, tea.KeyMsg) (bool, tea.Cmd)) {
	m.keyHandler = fn
}

// SetServerMessageHandler sets the server message handler function.
func (m *OnlineModel) SetServerMessageHandler(fn func(Model, *protocol.Message) tea.Cmd) {
	m.serverMessageHandler = fn
}

func (m *OnlineModel) connectingView() string {
	var sb string
	if m.error != "" {
		sb = common.ErrorStyle.Render(m.error)
	} else {
		sb = "正在连接服务器..."
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, sb)
}

func (m *OnlineModel) matchingView() string {
	elapsed := time.Since(m.matchingStartTime).Seconds()
	msg := fmt.Sprintf("🔍 正在匹配玩家...\n\n已等待: %.0f 秒\n\n按 ESC 取消", elapsed)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, msg)
}
