package ai

import (
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

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

// GameContext LLM 决策所需的游戏状态
type GameContext struct {
	BotID          string
	IsLandlord     bool
	Hand           []card.Card
	BottomCards    []card.Card
	RecentPlays    [2]PlayRecord // [0]=上家(最近), [1]=上上家
	MustPlay       bool
	CanBeat        bool
	PlayerCounts   [2]int            // 其他两名玩家的剩余牌数（按座位顺序）
	PlayerRoles    [2]bool           // 对应 PlayerCounts 的角色，true=地主
	RemainingCards map[card.Rank]int // 对手手中剩余各点数牌数
}
