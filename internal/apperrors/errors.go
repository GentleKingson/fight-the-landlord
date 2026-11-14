package apperrors

import (
	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

// GameError 游戏错误（房间和会话共享）
type GameError struct {
	Code    int
	Message string
}

func (e *GameError) Error() string {
	return e.Message
}

// newGameError 创建游戏错误，从 protocol.ErrorMessages 获取消息
func newGameError(code int) *GameError {
	message := protocol.ErrorMessages[code]
	if message == "" {
		message = protocol.ErrorMessages[protocol.ErrCodeUnknown]
	}
	return &GameError{Code: code, Message: message}
}

// 预定义错误
var (
	ErrAlreadyInRoom = &GameError{Code: protocol.ErrCodeUnknown, Message: "已在房间中，请先离开当前房间"}
	ErrRoomNotFound  = newGameError(protocol.ErrCodeRoomNotFound)
	ErrRoomFull      = newGameError(protocol.ErrCodeRoomFull)
	ErrNotInRoom     = newGameError(protocol.ErrCodeNotInRoom)
	ErrGameStarted   = newGameError(protocol.ErrCodeGameStarted)
	ErrGameNotStart  = newGameError(protocol.ErrCodeGameNotStart)
	ErrNotYourTurn   = newGameError(protocol.ErrCodeNotYourTurn)
	ErrInvalidCards  = newGameError(protocol.ErrCodeInvalidCards)
	ErrCannotBeat      = newGameError(protocol.ErrCodeCannotBeat)
	ErrMustPlay        = newGameError(protocol.ErrCodeMustPlay)
	ErrNotRoomOwner    = &GameError{Code: protocol.ErrCodeUnknown, Message: "只有房主可以开始游戏"}
	ErrPlayersNotReady = &GameError{Code: protocol.ErrCodeUnknown, Message: "请等待其他玩家准备后再开始游戏"}
	ErrPlayerOffline   = &GameError{Code: protocol.ErrCodeUnknown, Message: "房间内有玩家离线，请等待其重连后再开始游戏"}
)
