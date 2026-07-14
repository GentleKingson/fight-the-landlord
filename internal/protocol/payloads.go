package protocol

// --- 客户端请求 Payloads ---

// ReconnectPayload 断线重连请求
type ReconnectPayload struct {
	Token    string `json:"token"`     // 重连令牌
	PlayerID string `json:"player_id"` // 玩家 ID
}

// PingPayload 心跳请求
type PingPayload struct {
	Timestamp int64 `json:"timestamp"` // 客户端时间戳（毫秒）
}

// JoinRoomPayload 加入房间请求
type JoinRoomPayload struct {
	RoomCode string `json:"room_code"`
}

// BidPayload 叫地主请求
type BidPayload struct {
	Bid bool `json:"bid"` // true = 叫地主, false = 不叫
}

// PlayCardsPayload 出牌请求
type PlayCardsPayload struct {
	Cards []CardInfo `json:"cards"`
}

// GetLeaderboardPayload 获取排行榜请求
type GetLeaderboardPayload struct {
	Type   string `json:"type"`   // total/daily/weekly
	Offset int    `json:"offset"` // 偏移量
	Limit  int    `json:"limit"`  // 数量
}

// --- 服务端响应 Payloads ---

// ConnectedPayload 连接成功响应
type ConnectedPayload struct {
	PlayerID       string `json:"player_id"`
	PlayerName     string `json:"player_name"`
	ReconnectToken string `json:"reconnect_token"` // 重连令牌
}

// ReconnectedPayload 重连成功响应
type ReconnectedPayload struct {
	PlayerID       string        `json:"player_id"`
	PlayerName     string        `json:"player_name"`
	RoomCode       string        `json:"room_code,omitempty"`       // 如果在房间中
	GameState      *GameStateDTO `json:"game_state,omitempty"`      // 如果在游戏中
	ReconnectToken string        `json:"reconnect_token,omitempty"` // 服务端确认并旋转后的令牌
}

// GameStateDTO 游戏状态数据传输对象（用于重连恢复）
type GameStateDTO struct {
	Phase               string              `json:"phase"`                 // waiting/bidding/playing/ended
	Players             []PlayerInfo        `json:"players"`               // 所有玩家信息
	Hand                []CardInfo          `json:"hand"`                  // 自己的手牌
	BottomCards         []CardInfo          `json:"bottom_cards"`          // 已公开的底牌
	CurrentTurn         string              `json:"current_turn"`          // 当前回合玩家 ID
	LastPlayed          []CardInfo          `json:"last_played"`           // 有效上一手
	LastPlayerID        string              `json:"last_player_id"`        // 有效上一手玩家 ID
	MustPlay            bool                `json:"must_play"`             // 是否必须出牌
	CanBeat             bool                `json:"can_beat"`              // 是否能打过
	SnapshotVersion     int64               `json:"snapshot_version"`      // 单调递增版本
	GameID              string              `json:"game_id"`               // 当前牌局 ID
	BottomCardsRevealed bool                `json:"bottom_cards_revealed"` // 底牌是否公开
	TurnID              int64               `json:"turn_id"`               // 当前回合序号
	TurnDeadlineMS      int64               `json:"turn_deadline_ms"`      // 服务端绝对截止时间
	ServerTimeMS        int64               `json:"server_time_ms"`        // 快照生成时间
	LastPlayerName      string              `json:"last_player_name"`      // 有效上一手玩家名称
	LastHandType        string              `json:"last_hand_type"`        // 有效上一手牌型
	IsGrab              bool                `json:"is_grab"`               // 是否处于抢地主阶段
	Multiplier          int                 `json:"multiplier"`            // 当前倍数
	BaseScore           int                 `json:"base_score"`            // 本局底分
	PlayedCards         []PlayerPlayedCards `json:"played_cards"`          // 已公开出牌账本
}

// PlayerPlayedCards 某位玩家本局已经公开打出的牌。
type PlayerPlayedCards struct {
	PlayerID string     `json:"player_id"`
	Cards    []CardInfo `json:"cards"`
}

// PongPayload 心跳响应
type PongPayload struct {
	ClientTimestamp int64 `json:"client_timestamp"` // 客户端发送的时间戳
	ServerTimestamp int64 `json:"server_timestamp"` // 服务器时间戳（毫秒）
}

// PlayerOfflinePayload 玩家掉线通知
type PlayerOfflinePayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
	Timeout    int    `json:"timeout"` // 等待重连超时（秒）
}

// PlayerOnlinePayload 玩家上线通知
type PlayerOnlinePayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
}

// OnlineCountPayload 在线人数更新
type OnlineCountPayload struct {
	Count int `json:"count"` // 当前在线人数
}

// RoomCreatedPayload 房间创建成功响应
type RoomCreatedPayload struct {
	RoomCode string     `json:"room_code"`
	Player   PlayerInfo `json:"player"`
}

// RoomJoinedPayload 加入房间成功响应
type RoomJoinedPayload struct {
	RoomCode string       `json:"room_code"`
	Player   PlayerInfo   `json:"player"`
	Players  []PlayerInfo `json:"players"` // 房间内所有玩家
}

// PlayerJoinedPayload 其他玩家加入通知
type PlayerJoinedPayload struct {
	Player PlayerInfo `json:"player"`
}

// PlayerLeftPayload 玩家离开通知
type PlayerLeftPayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
}

// PlayerReadyPayload 玩家准备通知
type PlayerReadyPayload struct {
	PlayerID string `json:"player_id"`
	Ready    bool   `json:"ready"`
}

// GameStartPayload 游戏开始通知
type GameStartPayload struct {
	Players []PlayerInfo `json:"players"` // 按座位顺序排列
}

// DealCardsPayload 发牌通知
type DealCardsPayload struct {
	Cards       []CardInfo `json:"cards"`        // 玩家自己的手牌
	BottomCards []CardInfo `json:"bottom_cards"` // 底牌（地主确定后才显示具体内容）
}

// BidTurnPayload 轮到叫地主通知
type BidTurnPayload struct {
	PlayerID   string `json:"player_id"`
	Timeout    int    `json:"timeout"`    // 超时时间（秒）
	IsGrab     bool   `json:"is_grab"`    // true=抢地主阶段, false=叫地主阶段
	Multiplier int    `json:"multiplier"` // 当前倍数
}

// BidResultPayload 叫地主结果通知
type BidResultPayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
	Bid        bool   `json:"bid"`        // 是否叫/抢
	IsGrab     bool   `json:"is_grab"`    // 该决策是否处于抢地主阶段
	Multiplier int    `json:"multiplier"` // 决策后的当前倍数
}

// LandlordPayload 地主确定通知
type LandlordPayload struct {
	PlayerID    string     `json:"player_id"`
	PlayerName  string     `json:"player_name"`
	BottomCards []CardInfo `json:"bottom_cards"` // 底牌
	Multiplier  int        `json:"multiplier"`   // 底倍（叫抢结束后的倍数）
}

// PlayTurnPayload 轮到出牌通知
type PlayTurnPayload struct {
	PlayerID string `json:"player_id"`
	Timeout  int    `json:"timeout"`   // 超时时间（秒）
	MustPlay bool   `json:"must_play"` // 是否必须出牌（新一轮开始时为 true）
	CanBeat  bool   `json:"can_beat"`  // 是否有牌能打过上家
}

// CardPlayedPayload 出牌通知
type CardPlayedPayload struct {
	PlayerID   string     `json:"player_id"`
	PlayerName string     `json:"player_name"`
	Cards      []CardInfo `json:"cards"`
	CardsLeft  int        `json:"cards_left"` // 剩余手牌数
	HandType   string     `json:"hand_type"`  // 牌型名称
}

// PlayerPassPayload 不出通知
type PlayerPassPayload struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
}

// GameOverPayload 游戏结束通知
type GameOverPayload struct {
	WinnerID    string        `json:"winner_id"`
	WinnerName  string        `json:"winner_name"`
	IsLandlord  bool          `json:"is_landlord"`  // 获胜者是否是地主
	PlayerHands []PlayerHand  `json:"player_hands"` // 所有玩家剩余手牌
	Multiplier  int           `json:"multiplier"`   // 最终倍数
	Scores      []PlayerScore `json:"scores"`       // 每位玩家本局得分
}

// PlayerHand 玩家手牌信息（用于游戏结束展示）
type PlayerHand struct {
	PlayerID   string     `json:"player_id"`
	PlayerName string     `json:"player_name"`
	Cards      []CardInfo `json:"cards"`
}

// PlayerScore 玩家本局得分（用于游戏结束结算）
type PlayerScore struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
	IsLandlord bool   `json:"is_landlord"` // 是否是地主
	Score      int    `json:"score"`       // 本局得分（输为负，赢为正）
}

// MaintenancePayload 维护模式通知
type MaintenancePayload struct {
	Maintenance bool `json:"maintenance"` // 是否在维护模式
}

// MaintenanceStatusPayload 维护状态响应
type MaintenanceStatusPayload struct {
	Maintenance bool `json:"maintenance"` // 是否在维护模式
}

// ErrorPayload 错误响应
type ErrorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// StatsResultPayload 个人统计结果
type StatsResultPayload struct {
	PlayerID      string  `json:"player_id"`
	PlayerName    string  `json:"player_name"`
	TotalGames    int     `json:"total_games"`
	Wins          int     `json:"wins"`
	Losses        int     `json:"losses"`
	WinRate       float64 `json:"win_rate"`
	LandlordGames int     `json:"landlord_games"`
	LandlordWins  int     `json:"landlord_wins"`
	FarmerGames   int     `json:"farmer_games"`
	FarmerWins    int     `json:"farmer_wins"`
	Score         int     `json:"score"`
	Rank          int     `json:"rank"`
	CurrentStreak int     `json:"current_streak"`
	MaxWinStreak  int     `json:"max_win_streak"`
}

// LeaderboardResultPayload 排行榜结果
type LeaderboardResultPayload struct {
	Type    string             `json:"type"` // total/daily/weekly
	Entries []LeaderboardEntry `json:"entries"`
}

// LeaderboardEntry 排行榜条目
type LeaderboardEntry struct {
	Rank       int     `json:"rank"`
	PlayerID   string  `json:"player_id"`
	PlayerName string  `json:"player_name"`
	Score      int     `json:"score"`
	Wins       int     `json:"wins"`
	WinRate    float64 `json:"win_rate"`
}

// RoomListResultPayload 房间列表结果
type RoomListResultPayload struct {
	Rooms []RoomListItem `json:"rooms"`
}

// RoomListItem 房间列表项
type RoomListItem struct {
	RoomCode    string `json:"room_code"`
	PlayerCount int    `json:"player_count"`
	MaxPlayers  int    `json:"max_players"`
}

// ChatPayload 聊天消息
type ChatPayload struct {
	SenderID   string `json:"sender_id,omitempty"`   // 发送者 ID (服务端填充)
	SenderName string `json:"sender_name,omitempty"` // 发送者名字 (服务端填充)
	Content    string `json:"content"`               // 消息内容
	Scope      string `json:"scope"`                 // lobby/room/game
	Time       int64  `json:"time,omitempty"`        // 兼容字段，Unix 秒
	IsSystem   bool   `json:"is_system,omitempty"`   // 是否是系统消息
	MessageID  string `json:"message_id,omitempty"`  // 客户端生成、服务端回显的稳定 ID
	RoomCode   string `json:"room_code,omitempty"`   // 服务端确认的房间
	GameID     string `json:"game_id,omitempty"`     // 服务端确认的牌局
	ServerTime int64  `json:"server_time,omitempty"` // 服务端 Unix 毫秒
}

// --- 通用数据结构 ---

// PlayerInfo 玩家信息
type PlayerInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Seat       int    `json:"seat"`             // 座位号 0-2
	Ready      bool   `json:"ready"`            // 是否准备
	IsLandlord bool   `json:"is_landlord"`      // 是否是地主
	CardsCount int    `json:"cards_count"`      // 手牌数量
	Online     bool   `json:"online"`           // 是否在线
	IsBot      bool   `json:"is_bot,omitempty"` // 是否是机器人
}

// CardInfo 牌信息
type CardInfo struct {
	Suit  int `json:"suit"`  // 花色: 0=黑桃, 1=红心, 2=梅花, 3=方块, 4=王
	Rank  int `json:"rank"`  // 点数: 3-17 (3-2, 小王=16, 大王=17)
	Color int `json:"color"` // 颜色: 0=黑, 1=红
}
