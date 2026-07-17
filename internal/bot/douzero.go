package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
		httpClient: &http.Client{
			Timeout: douzeroTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
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
	return e.decidePlayWithProvenance(ctx, botName, gctx).cards
}

func (e *DouZeroEngine) decidePlayWithProvenance(ctx context.Context, botName string, gctx GameContext) playDecisionResult {
	if !gctx.MustPlay && !gctx.CanBeat {
		return playDecisionResult{}
	}

	if gctx.DouZeroPos == "" {
		log.Printf("🎮 [DouZero] %s: 位置未知，回退规则出牌", botName)
		e.recordFallback()
		return playDecisionResult{cards: ruleFallback(gctx), usedRuleFallback: true}
	}

	req := e.buildRequest(gctx)
	action, err := e.callService(ctx, req)
	if err != nil {
		reason := invalidActionHTTPError
		var invalidErr *douzeroActionError
		if errors.As(err, &invalidErr) {
			reason = invalidErr.reason
		}
		if e.metrics != nil && reason == invalidActionTimeout {
			e.metrics.BotTimeout("douzero")
		}
		return playDecisionResult{cards: e.invalidFallback(botName, reason, gctx), usedRuleFallback: true}
	}

	if len(action) == 0 {
		if gctx.MustPlay {
			return playDecisionResult{cards: e.invalidFallback(botName, invalidActionMustPlayPass, gctx), usedRuleFallback: true}
		}
		log.Printf("🎮 [DouZero] %s: pass", botName)
		return playDecisionResult{}
	}

	cards := e.douzeroToCards(action, gctx.Hand)
	if cards == nil {
		return playDecisionResult{cards: e.invalidFallback(botName, invalidActionNotOwned, gctx), usedRuleFallback: true}
	}

	parsed, parseErr := rule.ParseHand(cards)
	if parseErr != nil || parsed.Type == rule.Invalid {
		return playDecisionResult{cards: e.invalidFallback(botName, invalidActionInvalidHand, gctx), usedRuleFallback: true}
	}
	if !gctx.MustPlay && !gctx.RecentPlays[0].Played.IsEmpty() &&
		!rule.CanBeat(parsed, gctx.RecentPlays[0].Played) {
		return playDecisionResult{cards: e.invalidFallback(botName, invalidActionCannotBeat, gctx), usedRuleFallback: true}
	}

	log.Printf("🎮 [DouZero] %s 出牌: count=%d type=%s", botName, len(cards), parsed.Type.String())
	return playDecisionResult{cards: cards}
}

func (e *DouZeroEngine) invalidFallback(botName string, reason invalidActionReason, gctx GameContext) []card.Card {
	e.RecordInvalidAction(reason)
	e.recordFallback()
	// Only the bounded reason is logged. In particular, never include the
	// external response or the bot's complete hand.
	log.Printf("🎮 [DouZero] %s: invalid action (%s), using rule fallback", botName, reason)
	return ruleFallback(gctx)
}

func (e *DouZeroEngine) RecordInvalidAction(reason invalidActionReason) {
	if e != nil && e.metrics != nil {
		e.metrics.BotInvalidAction(string(reason))
	}
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

type douzeroActionError struct {
	reason invalidActionReason
}

func (e *douzeroActionError) Error() string {
	return string(e.reason)
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
		return nil, &douzeroActionError{reason: invalidActionDecodeError}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.serviceURL+"/decide_play", bytes.NewReader(body))
	if err != nil {
		return nil, &douzeroActionError{reason: invalidActionHTTPError}
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		reason := invalidActionHTTPError
		if isTimeoutError(err) {
			reason = invalidActionTimeout
		}
		return nil, &douzeroActionError{reason: reason}
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &douzeroActionError{reason: invalidActionHTTPError}
	}

	var result douzeroResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&result); err != nil {
		return nil, &douzeroActionError{reason: invalidActionDecodeError}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, &douzeroActionError{reason: invalidActionDecodeError}
	}

	if result.Error != "" {
		return nil, &douzeroActionError{reason: invalidActionHTTPError}
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
