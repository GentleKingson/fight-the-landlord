package session

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
)

type SnapshotPlayer struct {
	GamePlayer
	IsBot bool
}

// Snapshot is the private, versioned authority. Never use it as a client DTO.
type Snapshot struct {
	ActionReceipts                                                    map[string]string
	Players                                                           []SnapshotPlayer
	State                                                             GameState
	Config                                                            config.GameConfig
	CreatedAt                                                         time.Time
	AllOfflineAt                                                      time.Time
	EndedAt                                                           time.Time
	Deck                                                              card.Deck
	BottomCards                                                       []card.Card
	PublicPlays                                                       []protocol.PublicPlay
	Bids                                                              []protocol.BidResultPayload
	LastResult                                                        *protocol.GameOverPayload
	CurrentBidder, LandlordCaller, LandlordCandidate, BidPasses       int
	DeclinedCall, GrabDecided                                         [3]bool
	BidMultiplier, RedealCount, BombCount, LandlordPlays, FarmerPlays int
	CurrentPlayer, LastPlayerIdx, ConsecutivePasses                   int
	LastPlayedHand                                                    rule.ParsedHand
	DealID, BidToken, PlayToken                                       string
	DealStartAt, DealEndAt                                            time.Time
	TurnID                                                            uint64
	TimerMode                                                         string
	TimerStartTime                                                    time.Time
	RemainingTime                                                     time.Duration
	OfflineDeadline                                                   time.Time
}

func (gs *GameSession) snapshotLocked() Snapshot {
	var result *protocol.GameOverPayload
	if gs.lastResult != nil {
		result, _ = codec.ParsePayload[protocol.GameOverPayload](gs.lastResult)
	}
	players := make([]SnapshotPlayer, len(gs.players))
	for i, p := range gs.players {
		players[i] = SnapshotPlayer{GamePlayer: *p, IsBot: gs.room.Players[p.ID].Client.IsBot()}
	}
	return Snapshot{
		ActionReceipts: gs.actionReceipts,
		Players:        players, State: gs.state, Config: gs.gameConfig, CreatedAt: gs.room.CreatedAt,
		AllOfflineAt: gs.allOfflineAt, EndedAt: gs.endedAt,
		Deck: gs.deck, BottomCards: gs.bottomCards, PublicPlays: gs.publicPlays, Bids: gs.bidHistory, LastResult: result,
		CurrentBidder: gs.currentBidder, LandlordCaller: gs.landlordCaller, LandlordCandidate: gs.landlordCandidate,
		BidPasses: gs.bidPasses, DeclinedCall: gs.declinedCall, GrabDecided: gs.grabDecided,
		BidMultiplier: gs.bidMultiplier, RedealCount: gs.redealCount, BombCount: gs.bombCount,
		LandlordPlays: gs.landlordPlays, FarmerPlays: gs.farmerPlays,
		CurrentPlayer: gs.currentPlayer, LastPlayerIdx: gs.lastPlayerIdx, ConsecutivePasses: gs.consecutivePasses,
		LastPlayedHand: gs.lastPlayedHand, DealID: gs.dealID, BidToken: gs.bidToken, PlayToken: gs.playToken,
		DealStartAt: gs.dealStartAt, DealEndAt: gs.dealEndAt, TurnID: gs.turnID,
		TimerMode: gs.timerMode, TimerStartTime: gs.timerStartTime, RemainingTime: gs.remainingTime,
		OfflineDeadline: gs.offlineDeadline,
	}
}

func (gs *GameSession) loadSnapshot(s Snapshot) {
	gs.actionReceipts = s.ActionReceipts
	gs.players = make([]*GamePlayer, len(s.Players))
	for i := range s.Players {
		gs.players[i] = &s.Players[i].GamePlayer
	}
	gs.state, gs.gameConfig = s.State, s.Config
	gs.allOfflineAt, gs.endedAt = s.AllOfflineAt, s.EndedAt
	gs.deck, gs.bottomCards = s.Deck, s.BottomCards
	gs.publicPlays, gs.bidHistory = s.PublicPlays, s.Bids
	if s.LastResult != nil {
		gs.lastResult = codec.MustNewMessage(protocol.MsgGameOver, *s.LastResult)
	}
	gs.currentBidder, gs.landlordCaller, gs.landlordCandidate = s.CurrentBidder, s.LandlordCaller, s.LandlordCandidate
	gs.bidPasses, gs.declinedCall, gs.grabDecided = s.BidPasses, s.DeclinedCall, s.GrabDecided
	gs.bidMultiplier, gs.redealCount, gs.bombCount = s.BidMultiplier, s.RedealCount, s.BombCount
	gs.landlordPlays, gs.farmerPlays = s.LandlordPlays, s.FarmerPlays
	gs.currentPlayer, gs.lastPlayerIdx, gs.consecutivePasses = s.CurrentPlayer, s.LastPlayerIdx, s.ConsecutivePasses
	gs.lastPlayedHand = s.LastPlayedHand
	gs.dealID, gs.bidToken, gs.playToken = s.DealID, s.BidToken, s.PlayToken
	gs.dealStartAt, gs.dealEndAt, gs.turnID = s.DealStartAt, s.DealEndAt, s.TurnID
	gs.timerMode, gs.timerStartTime, gs.remainingTime = s.TimerMode, s.TimerStartTime, s.RemainingTime
	gs.offlineDeadline = s.OfflineDeadline
}

func DecodeSnapshot(record *storage.GameRecord) (Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(record.State, &s); err != nil {
		return s, err
	}
	if len(s.Players) != 3 || s.State < GameStateBidding || s.State > GameStateDealing || s.DealID == "" {
		return s, fmt.Errorf("invalid game state %s", record.ID)
	}
	if record.Ended != (s.State == GameStateEnded) || (record.Ended && s.LastResult == nil) {
		return s, fmt.Errorf("invalid settlement state %s", record.ID)
	}
	if err := validateSnapshotSeats(s); err != nil {
		return s, err
	}
	if err := validateSnapshotCards(s); err != nil {
		return s, err
	}
	return s, nil
}

func validateSnapshotSeats(s Snapshot) error {
	for _, index := range []int{s.CurrentPlayer, s.CurrentBidder, s.LastPlayerIdx} {
		if index < 0 || index >= 3 {
			return fmt.Errorf("invalid current seat")
		}
	}
	if s.LandlordCandidate < -1 || s.LandlordCandidate > 2 || s.LandlordCaller < -1 || s.LandlordCaller > 2 {
		return fmt.Errorf("invalid bidding state")
	}
	return nil
}

func validateSnapshotCards(s Snapshot) error {
	ids := make(map[string]bool)
	cards := make(map[card.Card]bool)
	valid := make(map[card.Card]bool)
	for _, c := range card.NewDeck() {
		valid[c] = true
	}
	all := make([]card.Card, 0, 54)
	for i, p := range s.Players {
		if p.ID == "" || ids[p.ID] || p.Seat != i {
			return fmt.Errorf("invalid snapshot player")
		}
		ids[p.ID] = true
		all = append(all, p.Hand...)
	}
	for _, play := range s.PublicPlays {
		for _, c := range play.Cards {
			all = append(all, card.Card{Suit: card.Suit(c.Suit), Rank: card.Rank(c.Rank), Color: card.CardColor(c.Color)})
		}
	}
	if s.State == GameStateDealing || s.State == GameStateBidding {
		all = append(all, s.BottomCards...)
	}
	for _, c := range all {
		if !valid[c] || cards[c] {
			return fmt.Errorf("invalid or duplicate snapshot card")
		}
		cards[c] = true
	}
	if len(cards) != 54 {
		return fmt.Errorf("snapshot must account for all 54 cards")
	}
	return nil
}
