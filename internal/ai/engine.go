package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
)

const llmTimeout = 15 * time.Second

// Engine LLM 决策引擎
type Engine struct {
	client openai.Client
	cfg    config.AIConfig
}

// NewEngine 创建 LLM 引擎
func NewEngine(cfg config.AIConfig) *Engine {
	client := openai.NewClient(
		option.WithBaseURL(cfg.BaseURL),
		option.WithAPIKey(cfg.APIKey),
	)
	return &Engine{client: client, cfg: cfg}
}

// DecidePlay 决定出什么牌，返回 nil 表示 pass
func (e *Engine) DecidePlay(ctx context.Context, botName string, gctx GameContext) []card.Card {
	if !e.cfg.Enabled || e.cfg.APIKey == "" {
		return rule.FindSmallestBeatingCards(gctx.Hand, gctx.RecentPlays[0].Played)
	}

	// 无牌可打时直接 pass，不消耗 token
	if !gctx.MustPlay && !gctx.CanBeat {
		log.Printf("🤖 AI %s 无牌可打，直接 pass", botName)
		return nil
	}

	userMsg := buildPlayPrompt(gctx)
	msgs := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(buildBasicPrompt()),
		openai.UserMessage(userMsg),
	}

	for attempt := 0; attempt < e.cfg.MaxRetries; attempt++ {
		tctx, cancel := context.WithTimeout(ctx, llmTimeout)
		raw, err := e.callLLM(tctx, msgs)
		cancel()

		if err != nil {
			log.Printf("🤖 LLM %s 调用失败 (attempt %d): %v", botName, attempt+1, err)
			break
		}

		cards, err := e.validateResponse(raw, gctx)
		if err == nil {
			if cards == nil {
				log.Printf("🤖 AI %s 选择 pass", botName)
			} else {
				log.Printf("🤖 AI %s 出牌: %s", botName, cardsToStr(cards))
			}
			return cards
		}

		log.Printf("🤖 LLM %s 输出校验失败 (attempt %d): %v, raw: %q", botName, attempt+1, err, raw)
		msgs[1] = openai.UserMessage(userMsg + fmt.Sprintf("\n[错误:%s，只输出合法牌型的牌面，不要输出解释]", err.Error()))
	}

	log.Printf("🤖 LLM 决策失败，使用本地兜底")
	return rule.FindSmallestBeatingCards(gctx.Hand, gctx.RecentPlays[0].Played)
}

// DecideBid 决定是否叫地主
func (e *Engine) DecideBid(ctx context.Context, botName string, hand []card.Card, prevBid *bool) bool {
	if !e.cfg.Enabled || e.cfg.APIKey == "" {
		return scoredBid(hand)
	}

	msgs := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(buildBidBasicPrompt()),
		openai.UserMessage(buildBidPrompt(hand, prevBid)),
	}

	tctx, cancel := context.WithTimeout(ctx, llmTimeout)
	raw, err := e.callLLM(tctx, msgs)
	cancel()

	if err != nil {
		log.Printf("🤖 LLM %s 叫地主失败: %v，使用启发式兜底", botName, err)
		return scoredBid(hand)
	}

	raw = strings.TrimSpace(raw)
	bid := strings.EqualFold(raw, "true")
	log.Printf("🤖 AI %s 叫地主: %v (raw: %q)", botName, bid, raw)
	return bid
}

func (e *Engine) callLLM(ctx context.Context, msgs []openai.ChatCompletionMessageParamUnion) (string, error) {
	if e.cfg.Debug {
		e.logMessages(msgs)
	}

	resp, err := e.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    e.cfg.Model,
		Messages: msgs,
		// 不限 token 数量，避免截断（出牌回复很短，成本可控）
	},
		// 禁用 deepseek-v4-flash 的思考模式（默认开启会导致 content 为空）
		option.WithJSONSet("thinking", map[string]string{"type": "disabled"}),
	)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("empty response")
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if content == "" {
		return "", errors.New("模型返回空内容（可能思考模式未完全关闭）")
	}

	if e.cfg.Debug {
		log.Printf("🤖 [AI DEBUG] assistant: %s", content)
	}

	return content, nil
}

func (e *Engine) logMessages(msgs []openai.ChatCompletionMessageParamUnion) {
	log.Printf("🤖 [AI DEBUG] ---- 请求开始（共 %d 条消息）----", len(msgs))
	for i, m := range msgs {
		// openai-go 的消息是联合类型，通过 JSON 序列化取出 role 和 content
		type msgShape struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		}
		raw, _ := m.MarshalJSON()
		var shape msgShape
		if err := json.Unmarshal(raw, &shape); err != nil {
			log.Printf("🤖 [AI DEBUG] [%d] (解析失败: %v)", i, err)
			continue
		}
		// 跳过 LLM 的 system 部分日志输出
		if shape.Role == "system" {
			continue
		}
		log.Printf("🤖 [AI DEBUG] [%d] %s: %v", i, shape.Role, shape.Content)
	}
	log.Printf("🤖 [AI DEBUG] ---- 请求结束 ----")
}

// validateResponse 校验 LLM 输出是否合法可出
func (e *Engine) validateResponse(raw string, gctx GameContext) ([]card.Card, error) {
	raw = strings.TrimSpace(raw)

	if strings.EqualFold(raw, "pass") {
		if gctx.MustPlay {
			return nil, errors.New("必须出牌不能pass")
		}
		return nil, nil
	}

	cards, err := parseCardNotation(raw, gctx.Hand)
	if err != nil {
		return nil, err
	}

	parsed, err := rule.ParseHand(cards)
	if err != nil || parsed.Type == rule.Invalid {
		return nil, errors.New("非法牌型")
	}

	if !gctx.RecentPlays[0].Played.IsEmpty() && !gctx.MustPlay {
		if !rule.CanBeat(parsed, gctx.RecentPlays[0].Played) {
			return nil, errors.New("无法压过上家")
		}
	}

	return cards, nil
}

// parseCardNotation 解析 LLM 输出的牌面字符串为具体 Card 切片
func parseCardNotation(raw string, hand []card.Card) ([]card.Card, error) {
	tokens := strings.Fields(strings.ToUpper(raw))
	if len(tokens) == 0 {
		return nil, errors.New("空输出")
	}

	rankCounts := make(map[card.Rank]int)
	for _, token := range tokens {
		var rank card.Rank
		var err error
		if token == "10" {
			rank = card.Rank10
		} else {
			rank, err = card.RankFromChar(rune(token[0]))
			if err != nil {
				return nil, fmt.Errorf("无效牌面: %s", token)
			}
		}
		rankCounts[rank]++
	}

	result := make([]card.Card, 0, len(tokens))
	used := make(map[int]bool)

	for rank, needed := range rankCounts {
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
			return nil, fmt.Errorf("手牌中%s不足(需要%d张)", rank, needed)
		}
	}

	return result, nil
}

// scoredBid 启发式叫地主决策
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
