package ai

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/palemoky/fight-the-landlord/internal/client"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
)

var botNames = []string{
	"运筹帷幄", "算无遗策", "天下无双", "牌技精湛", "料事如神",
	"出奇制胜", "胸有成竹", "稳操胜券", "势如破竹", "百战百胜",
}

// AIBotClient 实现 types.ClientInterface 的 AI 机器人
type AIBotClient struct {
	id     string
	name   string
	engine *Engine

	roomMu sync.RWMutex
	room   string

	sessionMu sync.RWMutex
	session   SessionInterface

	closedMu sync.RWMutex
	closed   bool

	state botState
}

type botState struct {
	mu            sync.RWMutex
	seat          int       // 本机器人的座位号（0-2）
	seatPlayerIDs [3]string // seatPlayerIDs[i] = 座位 i 的 playerID
	hand          []card.Card
	isLandlord    bool
	landlordID    string         // 地主的 playerID
	cardCounts    map[string]int // playerID → 剩余牌数
	orderedOthers [2]string      // [0]=上家 playerID, [1]=下家 playerID
	bottomCards   []card.Card
	recentPlays   [2]PlayRecord // [0]=最近一次出牌, [1]=上上次出牌
	prevBid       *bool         // 叫地主阶段上一个玩家的决策（nil=尚无）
	cardCounter   *client.CardCounter
}

// NewAIBotClient 创建 AI 机器人客户端
func NewAIBotClient(engine *Engine) *AIBotClient {
	name := fmt.Sprintf("🤖%s", botNames[rand.IntN(len(botNames))])
	return &AIBotClient{
		id:     uuid.New().String(),
		name:   name,
		engine: engine,
		state: botState{
			cardCounts:  make(map[string]int),
			cardCounter: client.NewCardCounter(),
		},
	}
}

// SetSession 在 GameSession 创建后注入（由 matcher 调用）
func (b *AIBotClient) SetSession(s SessionInterface) {
	b.sessionMu.Lock()
	defer b.sessionMu.Unlock()
	b.session = s
}

// --- types.ClientInterface 实现 ---

func (b *AIBotClient) GetID() string   { return b.id }
func (b *AIBotClient) GetName() string { return b.name }

func (b *AIBotClient) GetRoom() string {
	b.roomMu.RLock()
	defer b.roomMu.RUnlock()
	return b.room
}

func (b *AIBotClient) SetRoom(code string) {
	b.roomMu.Lock()
	defer b.roomMu.Unlock()
	b.room = code
}

func (b *AIBotClient) Close() {
	b.closedMu.Lock()
	defer b.closedMu.Unlock()
	b.closed = true
}

func (b *AIBotClient) IsBot() bool { return true }

func (b *AIBotClient) SendMessage(msg *protocol.Message) {
	b.closedMu.RLock()
	closed := b.closed
	b.closedMu.RUnlock()
	if closed {
		return
	}

	switch msg.Type {
	case protocol.MsgGameStart:
		b.handleGameStart(msg)
	case protocol.MsgDealCards:
		b.handleDealCards(msg)
	case protocol.MsgBidResult:
		b.handleBidResult(msg)
	case protocol.MsgLandlord:
		b.handleLandlord(msg)
	case protocol.MsgCardPlayed:
		b.handleCardPlayed(msg)
	case protocol.MsgBidTurn:
		go b.handleBidTurn(msg)
	case protocol.MsgPlayTurn:
		go b.handlePlayTurn(msg)
	}
}

// --- 消息处理 ---

func (b *AIBotClient) handleGameStart(msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.GameStartPayload](msg)
	if err != nil {
		log.Printf("🤖 handleGameStart decode error: %v", err)
		return
	}

	b.state.mu.Lock()
	defer b.state.mu.Unlock()

	b.state.cardCounts = make(map[string]int)

	for _, p := range payload.Players {
		b.state.cardCounts[p.ID] = 17
		b.state.seatPlayerIDs[p.Seat] = p.ID
		if p.ID == b.id {
			b.state.seat = p.Seat
		}
	}
	// 上家 = 前一座位（循环），下家 = 后一座位
	b.state.orderedOthers[0] = b.state.seatPlayerIDs[(b.state.seat+2)%3]
	b.state.orderedOthers[1] = b.state.seatPlayerIDs[(b.state.seat+1)%3]
	b.state.cardCounter.Reset()
	b.state.recentPlays = [2]PlayRecord{}
	b.state.prevBid = nil
	b.state.isLandlord = false
	b.state.landlordID = ""
}

func (b *AIBotClient) handleDealCards(msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.DealCardsPayload](msg)
	if err != nil {
		log.Printf("🤖 handleDealCards decode error: %v", err)
		return
	}

	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	b.state.hand = convert.InfosToCards(payload.Cards)
	log.Printf("🤖 %s 收到手牌 %d 张", b.name, len(b.state.hand))
}

func (b *AIBotClient) handleBidResult(msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.BidResultPayload](msg)
	if err != nil {
		log.Printf("🤖 handleBidResult decode error: %v", err)
		return
	}
	if payload.PlayerID == b.id {
		return // 自己的叫地主结果不需要记录为"上家"
	}
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	bid := payload.Bid
	b.state.prevBid = &bid
}

func (b *AIBotClient) handleLandlord(msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.LandlordPayload](msg)
	if err != nil {
		log.Printf("🤖 handleLandlord decode error: %v", err)
		return
	}

	b.state.mu.Lock()
	defer b.state.mu.Unlock()

	if payload.PlayerID == b.id {
		b.state.isLandlord = true
	}
	b.state.landlordID = payload.PlayerID
	// 更新地主的牌数（+3 底牌）
	if _, ok := b.state.cardCounts[payload.PlayerID]; ok {
		b.state.cardCounts[payload.PlayerID] += 3
	}
	b.state.bottomCards = convert.InfosToCards(payload.BottomCards)
}

func (b *AIBotClient) handleCardPlayed(msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.CardPlayedPayload](msg)
	if err != nil {
		log.Printf("🤖 handleCardPlayed decode error: %v", err)
		return
	}

	played := convert.InfosToCards(payload.Cards)

	b.state.mu.Lock()
	defer b.state.mu.Unlock()

	// 更新剩余牌数
	b.state.cardCounts[payload.PlayerID] = payload.CardsLeft

	// 如果是自己出的牌，从手牌中移除
	if payload.PlayerID == b.id {
		b.state.hand = removeCards(b.state.hand, played)
	}

	b.state.cardCounter.DeductCards(played)

	// 更新最近两次出牌（shift：旧的[0]→[1]，新的→[0]）
	parsed, parseErr := rule.ParseHand(played)
	if parseErr == nil && parsed.Type != rule.Invalid {
		b.state.recentPlays[1] = b.state.recentPlays[0]
		b.state.recentPlays[0] = PlayRecord{
			Played:     parsed,
			PlayerName: payload.PlayerName,
			IsLandlord: payload.PlayerID == b.state.landlordID,
		}
	}
}

func (b *AIBotClient) handleBidTurn(msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.BidTurnPayload](msg)
	if err != nil {
		log.Printf("🤖 handleBidTurn decode error: %v", err)
		return
	}
	if payload.PlayerID != b.id {
		return
	}

	time.Sleep(thinkDelay())

	b.state.mu.RLock()
	hand := make([]card.Card, len(b.state.hand))
	copy(hand, b.state.hand)
	prevBid := b.state.prevBid
	b.state.mu.RUnlock()

	bid := b.engine.DecideBid(context.Background(), b.name, hand, prevBid)

	b.sessionMu.RLock()
	sess := b.session
	b.sessionMu.RUnlock()

	if sess == nil {
		log.Printf("🤖 %s: session 未就绪，跳过叫地主", b.name)
		return
	}

	if err := sess.HandleBid(b.id, bid); err != nil {
		log.Printf("🤖 %s HandleBid 失败: %v", b.name, err)
	}
}

func (b *AIBotClient) handlePlayTurn(msg *protocol.Message) {
	payload, err := codec.ParsePayload[protocol.PlayTurnPayload](msg)
	if err != nil {
		log.Printf("🤖 handlePlayTurn decode error: %v", err)
		return
	}
	if payload.PlayerID != b.id {
		return
	}

	time.Sleep(thinkDelay())

	b.state.mu.RLock()
	gctx := b.buildGameContext(payload.MustPlay, payload.CanBeat)
	b.state.mu.RUnlock()

	b.sessionMu.RLock()
	sess := b.session
	b.sessionMu.RUnlock()

	if sess == nil {
		log.Printf("🤖 %s: session 未就绪，跳过出牌", b.name)
		return
	}

	cards := b.engine.DecidePlay(context.Background(), b.name, gctx)

	var playErr error
	if cards == nil {
		playErr = sess.HandlePass(b.id)
	} else {
		playErr = sess.HandlePlayCards(b.id, convert.CardsToInfos(cards))
	}

	if playErr != nil {
		log.Printf("🤖 %s 出牌失败: %v", b.name, playErr)
	}
}

// buildGameContext 构建 LLM 决策上下文（调用时需持有 state.mu.RLock）
func (b *AIBotClient) buildGameContext(mustPlay, canBeat bool) GameContext {
	hand := make([]card.Card, len(b.state.hand))
	copy(hand, b.state.hand)

	var counts [2]int
	var roles [2]bool
	pid0, pid1 := b.state.orderedOthers[0], b.state.orderedOthers[1]
	if pid0 != "" && pid1 != "" {
		counts[0], counts[1] = b.state.cardCounts[pid0], b.state.cardCounts[pid1]
		roles[0], roles[1] = pid0 == b.state.landlordID, pid1 == b.state.landlordID
	}

	return GameContext{
		IsLandlord:     b.state.isLandlord,
		Hand:           hand,
		BottomCards:    b.state.bottomCards,
		RecentPlays:    b.state.recentPlays,
		MustPlay:       mustPlay,
		CanBeat:        canBeat,
		PlayerCounts:   counts,
		PlayerRoles:    roles,
		RemainingCards: b.state.cardCounter.GetRemaining(),
	}
}

// thinkDelay 模拟思考时间（300–900ms）
func thinkDelay() time.Duration {
	return time.Duration(300+rand.IntN(600)) * time.Millisecond
}

// removeCards 从 hand 中移除 played 中的牌（按 Rank+Suit 精确匹配）
func removeCards(hand, played []card.Card) []card.Card {
	type key struct {
		suit int
		rank card.Rank
	}
	toRemove := make(map[key]int)
	for _, c := range played {
		toRemove[key{int(c.Suit), c.Rank}]++
	}
	result := make([]card.Card, 0, len(hand)-len(played))
	for _, c := range hand {
		k := key{int(c.Suit), c.Rank}
		if toRemove[k] > 0 {
			toRemove[k]--
		} else {
			result = append(result, c)
		}
	}
	return result
}
