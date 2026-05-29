package client

import (
	"cmp"
	"slices"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

// GameState 管理客户端侧的游戏状态
type GameState struct {
	// 玩家数据
	Hand        []card.Card
	BottomCards []card.Card
	IsLandlord  bool

	// 其他玩家
	Players []protocol.PlayerInfo

	// 游戏进程
	RoomCode       string
	CurrentTurn    string
	LastPlayedBy   string
	LastPlayedName string
	LastPlayed     []card.Card
	LastHandType   string

	// 叫抢地主 / 倍数
	Multiplier int  // 当前倍数（叫抢阶段为底倍，出牌阶段含炸弹等累计）
	IsGrabTurn bool // 当前叫地主轮是否处于抢地主阶段

	// 游戏结果
	Winner           string
	WinnerIsLandlord bool
	FinalMultiplier  int                    // 结算最终倍数
	Scores           []protocol.PlayerScore // 各玩家本局得分

	// 功能组件
	CardCounter *CardCounter
}

// NewGameState 创建一个新的游戏状态
func NewGameState() *GameState {
	return &GameState{
		CardCounter: NewCardCounter(),
	}
}

// SortHand 将玩家手牌按点数降序排序
func (gs *GameState) SortHand() {
	slices.SortFunc(gs.Hand, func(a, b card.Card) int {
		return cmp.Compare(b.Rank, a.Rank)
	})
}

// Reset 清除所有游戏状态
func (gs *GameState) Reset() {
	gs.Hand = nil
	gs.BottomCards = nil
	gs.Players = nil
	gs.RoomCode = ""
	gs.CurrentTurn = ""
	gs.LastPlayedBy = ""
	gs.LastPlayedName = ""
	gs.LastPlayed = nil
	gs.LastHandType = ""
	gs.IsLandlord = false
	gs.Multiplier = 0
	gs.IsGrabTurn = false
	gs.Winner = ""
	gs.WinnerIsLandlord = false
	gs.FinalMultiplier = 0
	gs.Scores = nil
	gs.CardCounter = NewCardCounter()
}
