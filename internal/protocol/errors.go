package protocol

// 错误码
const (
	ErrCodeUnknown           = 1000
	ErrCodeInvalidMsg        = 1001
	ErrCodeRateLimit         = 1002 // 速率限制
	ErrCodeReconnectInvalid  = 1003
	ErrCodeReconnectExpired  = 1004
	ErrCodeCommandCacheFull  = 1005
	ErrCodeRequestConflict   = 1006
	ErrCodeRoomNotFound      = 2001
	ErrCodeRoomFull          = 2002
	ErrCodeNotInRoom         = 2003
	ErrCodeGameStarted       = 2004 // 游戏已开始
	ErrCodeMatchNotQueued    = 2005 // 匹配已完成或未在队列中
	ErrCodeGameNotStart      = 3001
	ErrCodeNotYourTurn       = 3002
	ErrCodeInvalidCards      = 3003
	ErrCodeCannotBeat        = 3004
	ErrCodeMustPlay          = 3005
	ErrCodeStaleGame         = 3006
	ErrCodeStaleTurn         = 3007
	ErrCodeServerMaintenance = 5003 // 服务器维护中
	ErrCodeServerDraining    = 5004 // 服务器排空中
)

// ErrorMessages 错误码对应的消息
var ErrorMessages = map[int]string{
	ErrCodeUnknown:           "未知错误",
	ErrCodeInvalidMsg:        "无效的消息格式",
	ErrCodeRateLimit:         "请求过于频繁",
	ErrCodeReconnectInvalid:  "重连凭证无效",
	ErrCodeReconnectExpired:  "重连凭证已过期",
	ErrCodeCommandCacheFull:  "服务器正忙，请稍后重试",
	ErrCodeRequestConflict:   "request_id 已用于不同的命令",
	ErrCodeRoomNotFound:      "房间不存在",
	ErrCodeRoomFull:          "房间已满",
	ErrCodeNotInRoom:         "您不在房间中",
	ErrCodeGameStarted:       "游戏已开始",
	ErrCodeMatchNotQueued:    "匹配已完成或未在队列中",
	ErrCodeGameNotStart:      "游戏尚未开始",
	ErrCodeNotYourTurn:       "还没轮到您",
	ErrCodeInvalidCards:      "无效的牌型",
	ErrCodeCannotBeat:        "您的牌大不过上家",
	ErrCodeMustPlay:          "您必须出牌",
	ErrCodeStaleGame:         "牌局已更新，请同步后重试",
	ErrCodeStaleTurn:         "回合已更新，请同步后重试",
	ErrCodeServerMaintenance: "服务器维护中",
	ErrCodeServerDraining:    "服务器正在排空",
}
