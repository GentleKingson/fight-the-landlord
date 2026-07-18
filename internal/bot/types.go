package bot

import (
	"context"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

// DouZero position constants
const (
	DouZeroPosLandlord   = "landlord"
	DouZeroPosLandlordDn = "landlord_down"
	DouZeroPosLandlordUp = "landlord_up"
)

// DecisionEngine 决策引擎接口，规则启发式引擎和 DouZero 均实现此接口
type DecisionEngine interface {
	DecideBid(ctx context.Context, botName string, hand []card.Card, prevBid *bool) bool
	DecidePlay(ctx context.Context, botName string, gctx GameContext) []card.Card
}

type playDecisionResult struct {
	cards            []card.Card
	usedRuleFallback bool
}

type provenanceDecisionEngine interface {
	decidePlayWithProvenance(ctx context.Context, botName string, gctx GameContext) playDecisionResult
}

func decidePlayWithProvenance(engine DecisionEngine, ctx context.Context, botName string, gctx GameContext) playDecisionResult {
	if engineWithProvenance, ok := engine.(provenanceDecisionEngine); ok {
		return engineWithProvenance.decidePlayWithProvenance(ctx, botName, gctx)
	}
	return playDecisionResult{cards: engine.DecidePlay(ctx, botName, gctx)}
}

// SessionInterface 避免 session↔bot 循环依赖
type SessionInterface interface {
	HandleBid(playerID string, bid bool) error
	IsCurrentPlayTurn(playerID, gameID string, turnID int64) bool
	HandlePlayCardsAt(playerID string, cardInfos []protocol.CardInfo, gameID string, turnID int64) error
	HandlePassAt(playerID, gameID string, turnID int64) error
}

type invalidActionReason string

const (
	invalidActionTimeout        invalidActionReason = "timeout"
	invalidActionHTTPError      invalidActionReason = "http_error"
	invalidActionDecodeError    invalidActionReason = "decode_error"
	invalidActionNotOwned       invalidActionReason = "not_owned"
	invalidActionInvalidHand    invalidActionReason = "invalid_hand"
	invalidActionCannotBeat     invalidActionReason = "cannot_beat"
	invalidActionMustPlayPass   invalidActionReason = "must_play_pass"
	invalidActionStaleTurn      invalidActionReason = "stale_turn"
	invalidActionSubmitRejected invalidActionReason = "submit_rejected"
)

// PlayRecord 一次出牌记录
type PlayRecord struct {
	Played     rule.ParsedHand
	PlayerName string
	IsLandlord bool
}

// GameContext 决策引擎所需的游戏状态
type GameContext struct {
	IsLandlord     bool
	Hand           []card.Card
	BottomCards    []card.Card
	RecentPlays    [2]PlayRecord // [0]=上家(最近), [1]=上上家
	MustPlay       bool
	CanBeat        bool
	PlayerCounts   [2]int            // [0]=上家, [1]=下家 剩余牌数
	PlayerRoles    [2]bool           // 对应 PlayerCounts 的角色，true=地主
	RemainingCards map[card.Rank]int // 场上剩余各点数牌数（记牌器）

	// DouZero 引擎专用字段
	DouZeroPos   string         // "landlord"|"landlord_up"|"landlord_down"
	PlayedByPos  [3][]card.Rank // [0]=landlord,[1]=landlord_down,[2]=landlord_up 已出牌
	ActionSeq    [][]card.Rank  // 完整出牌序列，nil 元素表示 pass
	LastMovePos  string         // 上次出牌的 DouZero 位置
	NumCardsLeft map[string]int // DouZero 位置 → 剩余牌数
}
