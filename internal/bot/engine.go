package bot

import (
	"context"
	"log"
	"strings"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
)

// HeuristicEngine 规则启发式决策引擎。
// 完全基于本地规则推导，无任何外部依赖，始终只产出合法牌型。
type HeuristicEngine struct{}

// NewHeuristicEngine 创建规则启发式引擎
func NewHeuristicEngine() *HeuristicEngine {
	return &HeuristicEngine{}
}

// DecidePlay 决定出什么牌，返回 nil 表示 pass
func (e *HeuristicEngine) DecidePlay(_ context.Context, botName string, gctx GameContext) []card.Card {
	// 无牌可打时直接 pass
	if !gctx.MustPlay && !gctx.CanBeat {
		return nil
	}

	cards := ruleFallback(gctx)
	if cards == nil {
		log.Printf("🤖 %s 选择 pass", botName)
	} else {
		parsed, err := rule.ParseHand(cards)
		if err != nil {
			log.Printf("🤖 %s 出牌: count=%d", botName, len(cards))
		} else {
			log.Printf("🤖 %s 出牌: count=%d type=%s", botName, len(cards), parsed.Type.String())
		}
	}
	return cards
}

// ruleFallback produces one deterministic legal action for the authoritative
// turn context. A must-play turn starts a new trick, even if the bot still
// retains the previous non-pass play in its local history.
func ruleFallback(gctx GameContext) []card.Card {
	previous := gctx.RecentPlays[0].Played
	if gctx.MustPlay {
		previous = rule.ParsedHand{}
	}
	return rule.FindSmallestBeatingCards(gctx.Hand, previous)
}

// DecideBid 决定是否叫地主 / 抢地主
func (e *HeuristicEngine) DecideBid(_ context.Context, _ string, hand []card.Card, _ *bool) bool {
	return scoredBid(hand)
}

// scoredBid 启发式叫地主决策：根据大牌、炸弹给手牌打分，达到阈值则叫/抢
func scoredBid(hand []card.Card) bool {
	score := 0.0
	rankCounts := make(map[card.Rank]int)
	for _, c := range hand {
		rankCounts[c.Rank]++
	}
	for rank, count := range rankCounts {
		if count == 4 {
			score += 3
		}
		switch rank {
		case card.RankRedJoker:
			score += 2
		case card.RankBlackJoker:
			score += 1.5
		case card.Rank2:
			score += 1
		case card.RankA:
			score += 0.5
		}
	}
	return score >= 3.5
}

// cardsToStr 将牌切片格式化为以空格分隔的牌面字符串（日志用）
func cardsToStr(cards []card.Card) string {
	parts := make([]string, len(cards))
	for i, c := range cards {
		parts[i] = c.Rank.String()
	}
	return strings.Join(parts, " ")
}
