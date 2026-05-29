package ai

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

// SessionInterface 避免 session↔ai 循环依赖
type SessionInterface interface {
	HandleBid(playerID string, bid bool) error
	HandlePlayCards(playerID string, cardInfos []protocol.CardInfo) error
	HandlePass(playerID string) error
}

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
