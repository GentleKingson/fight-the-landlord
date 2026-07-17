package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/observability"
)

const douzeroTimeout = 5 * time.Second

// DouZeroEngine 调用 Python DouZero HTTP 服务做决策
type DouZeroEngine struct {
	serviceURL string
	httpClient *http.Client
	metrics    *observability.Metrics
}

func (e *DouZeroEngine) SetMetrics(metrics *observability.Metrics) {
	if e != nil {
		e.metrics = metrics
	}
}

// NewDouZeroEngine 创建 DouZero 引擎
func NewDouZeroEngine(serviceURL string) *DouZeroEngine {
	return &DouZeroEngine{
		serviceURL: serviceURL,
		httpClient: &http.Client{Timeout: douzeroTimeout},
	}
}

// rankToDouZero 将项目 Rank 转为 DouZero card int
func rankToDouZero(r card.Rank) int {
	switch r {
	case card.Rank2:
		return 17
	case card.RankBlackJoker:
		return 20
	case card.RankRedJoker:
		return 30
	default:
		return int(r)
	}
}

// douzeroToRank 将 DouZero card int 转为项目 Rank
func douzeroToRank(v int) card.Rank {
	switch v {
	case 17:
		return card.Rank2
	case 20:
		return card.RankBlackJoker
	case 30:
		return card.RankRedJoker
	default:
		return card.Rank(v)
	}
}

func cardsToDouZeroInts(cards []card.Card) []int {
	result := make([]int, len(cards))
	for i, c := range cards {
		result[i] = rankToDouZero(c.Rank)
	}
	return result
}

func ranksToDouZeroInts(ranks []card.Rank) []int {
	result := make([]int, len(ranks))
	for i, r := range ranks {
		result[i] = rankToDouZero(r)
	}
	return result
}

func parsedHandRanks(h rule.ParsedHand) []card.Rank {
	ranks := make([]card.Rank, len(h.Cards))
	for i, c := range h.Cards {
		ranks[i] = c.Rank
	}
	return ranks
}

type douzeroRequest struct {
	Position              string         `json:"position"`
	Hand                  []int          `json:"hand"`
	BottomCards           []int          `json:"bottom_cards"`
	PlayedCardsLandlord   []int          `json:"played_cards_landlord"`
	PlayedCardsLandlordDn []int          `json:"played_cards_landlord_down"`
	PlayedCardsLandlordUp []int          `json:"played_cards_landlord_up"`
	CardPlayActionSeq     [][]int        `json:"card_play_action_seq"`
	NumCardsLeft          map[string]int `json:"num_cards_left"`
	LastMove              []int          `json:"last_move"`
	LastMovePosition      string         `json:"last_move_position"`
	MustPlay              bool           `json:"must_play"`
}

type douzeroResponse struct {
	Action []int  `json:"action"`
	Error  string `json:"error,omitempty"`
}

func (e *DouZeroEngine) DecideBid(_ context.Context, _ string, hand []card.Card, _ *bool) bool {
	return scoredBid(hand)
}

func (e *DouZeroEngine) DecidePlay(ctx context.Context, botName string, gctx GameContext) []card.Card {
	if !gctx.MustPlay && !gctx.CanBeat {
		return nil
	}

	if gctx.DouZeroPos == "" {
		log.Printf("🎮 [DouZero] %s: 位置未知，回退规则出牌", botName)
		e.recordFallback()
		return rule.FindSmallestBeatingCards(gctx.Hand, gctx.RecentPlays[0].Played)
	}

	req := e.buildRequest(gctx)
	action, err := e.callService(ctx, req)
	if err != nil {
		log.Printf("🎮 [DouZero] %s: 服务错误: %v，回退规则出牌", botName, err)
		if e.metrics != nil && isTimeoutError(err) {
			e.metrics.BotTimeout("douzero")
		}
		e.recordFallback()
		return rule.FindSmallestBeatingCards(gctx.Hand, gctx.RecentPlays[0].Played)
	}

	if len(action) == 0 {
		if gctx.MustPlay {
			log.Printf("🎮 [DouZero] %s: 返回 pass 但必须出牌，回退规则出牌", botName)
			e.recordFallback()
			return rule.FindSmallestBeatingCards(gctx.Hand, gctx.RecentPlays[0].Played)
		}
		log.Printf("🎮 [DouZero] %s: pass", botName)
		return nil
	}

	cards := e.douzeroToCards(action, gctx.Hand)
	if cards == nil {
		log.Printf("🎮 [DouZero] %s: 牌面转换失败，回退规则出牌", botName)
		e.recordFallback()
		return rule.FindSmallestBeatingCards(gctx.Hand, gctx.RecentPlays[0].Played)
	}

	log.Printf("🎮 [DouZero] %s 出牌: %s", botName, cardsToStr(cards))
	return cards
}

func (e *DouZeroEngine) recordFallback() {
	if e != nil && e.metrics != nil {
		e.metrics.BotFallback("douzero", "heuristic")
	}
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func (e *DouZeroEngine) buildRequest(gctx GameContext) douzeroRequest {
	actionSeq := make([][]int, len(gctx.ActionSeq))
	for i, move := range gctx.ActionSeq {
		if move == nil {
			actionSeq[i] = []int{}
		} else {
			actionSeq[i] = ranksToDouZeroInts(move)
		}
	}

	var lastMove []int
	if !gctx.RecentPlays[0].Played.IsEmpty() && !gctx.MustPlay {
		lastMove = ranksToDouZeroInts(parsedHandRanks(gctx.RecentPlays[0].Played))
	}

	numCardsLeft := gctx.NumCardsLeft
	if numCardsLeft == nil {
		numCardsLeft = make(map[string]int)
	}

	return douzeroRequest{
		Position:              gctx.DouZeroPos,
		Hand:                  cardsToDouZeroInts(gctx.Hand),
		BottomCards:           cardsToDouZeroInts(gctx.BottomCards),
		PlayedCardsLandlord:   ranksToDouZeroInts(gctx.PlayedByPos[0]),
		PlayedCardsLandlordDn: ranksToDouZeroInts(gctx.PlayedByPos[1]),
		PlayedCardsLandlordUp: ranksToDouZeroInts(gctx.PlayedByPos[2]),
		CardPlayActionSeq:     actionSeq,
		NumCardsLeft:          numCardsLeft,
		LastMove:              lastMove,
		LastMovePosition:      gctx.LastMovePos,
		MustPlay:              gctx.MustPlay,
	}
}

func (e *DouZeroEngine) callService(ctx context.Context, req douzeroRequest) ([]int, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.serviceURL+"/decide_play", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	var result douzeroResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	if result.Error != "" {
		return nil, fmt.Errorf("service: %s", result.Error)
	}

	return result.Action, nil
}

func (e *DouZeroEngine) douzeroToCards(action []int, hand []card.Card) []card.Card {
	rankNeeded := make(map[card.Rank]int)
	for _, v := range action {
		rankNeeded[douzeroToRank(v)]++
	}

	result := make([]card.Card, 0, len(action))
	used := make(map[int]bool)

	for rank, needed := range rankNeeded {
		found := 0
		for i, c := range hand {
			if c.Rank == rank && !used[i] {
				result = append(result, c)
				used[i] = true
				found++
				if found == needed {
					break
				}
			}
		}
		if found < needed {
			return nil
		}
	}

	slices.SortFunc(result, func(a, b card.Card) int {
		return int(a.Rank) - int(b.Rank)
	})
	return result
}
