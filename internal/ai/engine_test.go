package ai

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
)

// --- 测试辅助函数 ---

// cards 从空格分隔的牌面字符串构建 []card.Card（花色统一用黑桃，不影响规则判断）
func cards(notation string) []card.Card {
	tokens := strings.Fields(strings.ToUpper(notation))
	result := make([]card.Card, 0, len(tokens))
	for _, token := range tokens {
		var rank card.Rank
		if token == "10" {
			rank = card.Rank10
		} else {
			r, err := card.RankFromChar(rune(token[0]))
			if err != nil {
				panic(fmt.Sprintf("无效牌面记号: %q", token))
			}
			rank = r
		}
		result = append(result, card.Card{Rank: rank, Suit: card.Spade})
	}
	return result
}

// play 从牌面字符串构建 PlayRecord
func play(notation string, isLandlord bool) PlayRecord {
	c := cards(notation)
	parsed, err := rule.ParseHand(c)
	if err != nil || parsed.Type == rule.Invalid {
		panic(fmt.Sprintf("无法解析出牌记录 %q: %v", notation, err))
	}
	return PlayRecord{Played: parsed, IsLandlord: isLandlord}
}

func TestHeuristicEngine_DecideBid(t *testing.T) {
	t.Parallel()
	e := NewHeuristicEngine()

	// 强牌（含双王 + 炸弹 + 2）应叫地主
	strong := cards("R B 2 2 2 2 A K")
	if !e.DecideBid(context.Background(), "bot", strong, nil) {
		t.Errorf("强牌应叫地主，scoredBid=%v", scoredBid(strong))
	}

	// 弱牌不应叫地主
	weak := cards("3 4 5 6 7 8 9 T")
	if e.DecideBid(context.Background(), "bot", weak, nil) {
		t.Errorf("弱牌不应叫地主，scoredBid=%v", scoredBid(weak))
	}
}

func TestHeuristicEngine_DecidePlay_PassWhenNoBeat(t *testing.T) {
	t.Parallel()
	e := NewHeuristicEngine()

	// 既非必须出牌、也无法压过上家 → pass
	gctx := GameContext{
		Hand:        cards("3 4 5"),
		RecentPlays: [2]PlayRecord{play("2", true), {}},
		MustPlay:    false,
		CanBeat:     false,
	}
	if got := e.DecidePlay(context.Background(), "bot", gctx); got != nil {
		t.Errorf("无牌可压时应 pass，却出了 %s", cardsToStr(got))
	}
}

func TestHeuristicEngine_DecidePlay_AlwaysLegal(t *testing.T) {
	t.Parallel()
	e := NewHeuristicEngine()

	// 自由出牌（必须出牌）：应给出一手合法牌型
	gctx := GameContext{
		Hand:        cards("3 3 4 5 6 7 8 9"),
		RecentPlays: [2]PlayRecord{},
		MustPlay:    true,
		CanBeat:     false,
	}
	got := e.DecidePlay(context.Background(), "bot", gctx)
	if got == nil {
		t.Fatal("必须出牌时不应 pass")
	}
	parsed, err := rule.ParseHand(got)
	if err != nil || parsed.Type == rule.Invalid {
		t.Errorf("启发式引擎产出了非法牌型: %s", cardsToStr(got))
	}

	// 跟牌：压过上家的单张，产出必须能压过且合法
	follow := GameContext{
		Hand:        cards("3 4 5 9 K 2"),
		RecentPlays: [2]PlayRecord{play("J", true), {}},
		MustPlay:    false,
		CanBeat:     true,
	}
	got = e.DecidePlay(context.Background(), "bot", follow)
	if got == nil {
		t.Fatal("有牌可压时不应 pass")
	}
	parsed, err = rule.ParseHand(got)
	if err != nil || parsed.Type == rule.Invalid {
		t.Fatalf("跟牌产出非法牌型: %s", cardsToStr(got))
	}
	if !rule.CanBeat(parsed, follow.RecentPlays[0].Played) {
		t.Errorf("跟牌 %s 无法压过上家 J", cardsToStr(got))
	}
}
