package handler

import (
	"fmt"
	"math/rand/v2"
	"time"

	"charm.land/bubbles/v2/timer"
	tea "charm.land/bubbletea/v2"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
	payloadconv "github.com/palemoky/fight-the-landlord/internal/protocol/convert/payload"
	"github.com/palemoky/fight-the-landlord/internal/ui/model"
)

// restoreGameState 用重连快照覆盖本地游戏状态，避免沿用掉线前的过期数据（例如上家出牌、各家剩余牌数、地主标记等）
func restoreGameState(m model.Model, dto *protocol.GameStateDTO) {
	st := m.Game().State()
	myID := m.PlayerID()

	// 玩家信息：剩余牌数、地主标记、在线状态
	st.Players = dto.Players

	// 自己的手牌
	st.Hand = convert.InfosToCards(dto.Hand)
	st.SortHand()

	// 自己是否地主（从权威的玩家信息里取，而非沿用旧标记）
	st.IsLandlord = false
	for _, p := range dto.Players {
		if p.ID == myID {
			st.IsLandlord = p.IsLandlord
			break
		}
	}

	// 底牌仅在出牌阶段（地主已确定）才揭晓；叫地主阶段保持隐藏，避免提前泄露
	if dto.Phase == "playing" {
		st.BottomCards = convert.InfosToCards(dto.BottomCards)
	} else {
		st.BottomCards = nil
	}

	// 当前回合
	st.CurrentTurn = dto.CurrentTurn

	// 上家出牌（DTO 不含出牌者名字与牌型，分别从玩家列表查名、按牌重新识别牌型）
	st.LastPlayed = convert.InfosToCards(dto.LastPlayed)
	st.LastPlayedBy = dto.LastPlayerID
	st.LastPlayedName = ""
	st.LastHandType = ""
	if len(st.LastPlayed) > 0 {
		for _, p := range dto.Players {
			if p.ID == dto.LastPlayerID {
				st.LastPlayedName = p.Name
				break
			}
		}
		if ph, err := rule.ParseHand(st.LastPlayed); err == nil {
			st.LastHandType = ph.Type.String()
		}
	}

	// 记牌器：快照不含完整出牌历史，只能按可见信息尽力重建
	// （自己的手牌、底牌(若为地主)、可见的上家牌）；后续出牌事件会继续修正。
	st.CardCounter.Reset()
	st.CardCounter.DeductCards(st.Hand)
	if st.IsLandlord {
		st.CardCounter.DeductCards(st.BottomCards)
	}
	if dto.LastPlayerID != myID {
		st.CardCounter.DeductCards(st.LastPlayed)
	}

	m.Game().SetMustPlay(dto.MustPlay)
}

func handleMsgGameStart(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.GameStartPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	m.Game().State().Players = payload.Players
	// 新一局重置自己的地主标记，避免沿用上一局导致手牌区误显示地主图标
	m.Game().State().IsLandlord = false
	return nil
}

func handleMsgDealCards(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.DealCardsPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	m.Game().State().Hand = convert.InfosToCards(payload.Cards)
	m.Game().State().SortHand()
	if len(payload.BottomCards) > 0 && payload.BottomCards[0].Rank > 0 {
		m.Game().State().BottomCards = convert.InfosToCards(payload.BottomCards)
	}

	for i := range m.Game().State().Players {
		m.Game().State().Players[i].CardsCount = 17
	}

	m.Game().State().CardCounter.Reset()
	m.Game().State().CardCounter.DeductCards(m.Game().State().Hand)

	// 新一局清空上一手出牌状态，避免跨局误判“压死”
	m.Game().State().LastPlayed = nil
	m.Game().State().LastPlayedBy = ""
	m.Game().State().LastPlayedName = ""
	m.Game().State().LastHandType = ""

	// 地主确定后服务端会再发一次 MsgDealCards 更新地主手牌（含底牌），
	// 此时已处于 PhaseBidding，不应重复播放发牌音效。
	if m.Phase() != model.PhaseBidding {
		m.StopBGM()
		m.PlaySound("deal")
	}
	return nil
}

func handleMsgBidTurn(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.BidTurnPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	m.SetPhase(model.PhaseBidding)
	m.Game().SetBidTurn(payload.PlayerID)
	m.Game().SetBellPlayed(false)
	m.Game().State().IsGrabTurn = payload.IsGrab
	m.Game().State().Multiplier = payload.Multiplier

	action := "叫地主"
	if payload.IsGrab {
		action = "抢地主"
	}
	if payload.PlayerID == m.PlayerID() {
		m.Input().Placeholder = fmt.Sprintf("%s? (Y/N)", action)
		m.Input().Focus()
	} else {
		for _, p := range m.Game().State().Players {
			if p.ID == payload.PlayerID {
				m.Input().Placeholder = fmt.Sprintf("等待 %s %s...", p.Name, action)
				break
			}
		}
		m.Input().Blur()
	}
	m.Game().SetTimerDuration(time.Duration(payload.Timeout) * time.Second)
	m.Game().SetTimerStartTime(time.Now())
	t := timer.New(m.Game().TimerDuration(), timer.WithInterval(time.Second))
	m.SetTimer(t)
	return t.Start()
}

func handleMsgBidResult(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.BidResultPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	// 同步当前倍数（抢地主会翻倍）
	m.Game().State().Multiplier = payload.Multiplier

	// 男声播报叫/抢结果
	switch {
	case !payload.IsGrab && payload.Bid:
		m.PlaySound("bid_call") // 叫地主
	case !payload.IsGrab && !payload.Bid:
		m.PlaySound("bid_nocall") // 不叫
	case payload.IsGrab && payload.Bid:
		// 抢地主有两个音效，随机播一个增加变化
		m.PlaySound(randVoice("bid_grab", "bid_grab2", "bid_grab3"))
	case payload.IsGrab && !payload.Bid:
		m.PlaySound("bid_nograb") // 不抢
	}
	return nil
}

// handleMsgPlayerPass 有人不出时随机播放一个“不出”男声
func handleMsgPlayerPass(m model.Model, _ *protocol.Message) tea.Cmd {
	m.PlaySound(randVoice("pass", "pass_buyao", "pass_guo", "pass_peng"))
	return nil
}

// randVoice 从给定的若干音效键名中随机返回一个
func randVoice(names ...string) string {
	return names[rand.IntN(len(names))]
}

func handleMsgLandlord(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.LandlordPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	m.Game().State().BottomCards = convert.InfosToCards(payload.BottomCards)
	for i, p := range m.Game().State().Players {
		m.Game().State().Players[i].IsLandlord = (p.ID == payload.PlayerID)
		if p.ID == payload.PlayerID {
			m.Game().State().Players[i].CardsCount = 20
		}
	}
	if payload.PlayerID == m.PlayerID() {
		m.Game().State().IsLandlord = true
		m.Game().State().CardCounter.DeductCards(m.Game().State().BottomCards)
	}
	m.Game().State().IsGrabTurn = false
	m.Game().State().Multiplier = payload.Multiplier

	m.PlaySound("landlord")
	// 地主确定、正式开打后才起背景音乐，避免与发牌声/叫牌语音重叠
	m.PlayBGM("bgm_normal")
	return nil
}

func handleMsgPlayTurn(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.PlayTurnPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	m.SetPhase(model.PhasePlaying)
	m.Game().State().CurrentTurn = payload.PlayerID
	m.Game().SetMustPlay(payload.MustPlay)
	m.Game().SetCanBeat(payload.CanBeat)
	m.Game().SetBellPlayed(false)
	if payload.PlayerID == m.PlayerID() {
		switch {
		case payload.MustPlay:
			m.Input().Placeholder = "你必须出牌 (如 33344)"
		case payload.CanBeat:
			m.Input().Placeholder = "出牌或 PASS"
		default:
			m.Input().Placeholder = "没有能大过上家的牌，输入 PASS"
		}
		m.Input().Focus()
	} else {
		for _, p := range m.Game().State().Players {
			if p.ID == payload.PlayerID {
				m.Input().Placeholder = fmt.Sprintf("等待 %s 出牌...", p.Name)
				break
			}
		}
		m.Input().Blur()
	}
	m.Game().SetTimerDuration(time.Duration(payload.Timeout) * time.Second)
	m.Game().SetTimerStartTime(time.Now())
	t := timer.New(m.Game().TimerDuration(), timer.WithInterval(time.Second))
	m.SetTimer(t)
	return t.Start()
}

func handleMsgCardPlayed(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.CardPlayedPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)
	// 判断是否“压死”：上一手非空且出自其他玩家，说明本次是接牌压过上家
	prevBy := m.Game().State().LastPlayedBy
	isBeat := len(m.Game().State().LastPlayed) > 0 && prevBy != "" && prevBy != payload.PlayerID
	m.Game().State().LastPlayedBy = payload.PlayerID
	m.Game().State().LastPlayedName = payload.PlayerName
	m.Game().State().LastPlayed = convert.InfosToCards(payload.Cards)
	m.Game().State().LastHandType = payload.HandType
	// 炸弹 / 王炸：本地同步倍数翻倍（与服务端结算一致）
	if payload.HandType == rule.Bomb.String() || payload.HandType == rule.Rocket.String() {
		m.Game().State().Multiplier = max(m.Game().State().Multiplier, 1) * 2
	}
	for i, p := range m.Game().State().Players {
		if p.ID == payload.PlayerID {
			m.Game().State().Players[i].CardsCount = payload.CardsLeft
			break
		}
	}
	if payload.PlayerID == m.PlayerID() {
		m.Game().State().Hand = card.RemoveCards(m.Game().State().Hand, m.Game().State().LastPlayed)
	} else {
		// 只记录其他玩家出的牌
		m.Game().State().CardCounter.DeductCards(m.Game().State().LastPlayed)
	}

	playCardPlayedSounds(m, payload, isBeat)
	return nil
}

// playCardPlayedSounds 根据本次出牌播放音效：出牌声/报牌或“压死”语音、剩牌提醒，并按场上剩牌切换 BGM。
func playCardPlayedSounds(m model.Model, payload protocol.CardPlayedPayload, isBeat bool) {
	m.PlaySound("play")
	switch {
	case isBeat && payload.HandType != rule.Bomb.String() && payload.HandType != rule.Rocket.String():
		// 普通接牌压过上家：优先报牌型（单/对/三张报点数），其间穿插“压死”男声
		switch payload.HandType {
		case rule.Single.String(), rule.Pair.String(), rule.Trio.String():
			// 2/3 概率报点数，1/3 概率播“压死”，让两种语音交替出现而非单调
			if rand.IntN(3) == 0 {
				m.PlaySound(randVoice("beat", "beat_bigger", "beat_cover"))
			} else {
				playCardVoice(m, payload.HandType, m.Game().State().LastPlayed)
			}
		default:
			// 其余牌型没有专门的报点数语音，仍用“压死”男声
			m.PlaySound(randVoice("beat", "beat_bigger", "beat_cover"))
		}
	default:
		// 首出 / 新一轮领出，或用炸弹、王炸压死：男声报牌（牌型 + 点数）
		playCardVoice(m, payload.HandType, m.Game().State().LastPlayed)
	}
	// 出完后剩 1/2 张时语音提醒
	switch payload.CardsLeft {
	case 1:
		m.PlaySound("last1")
	case 2:
		m.PlaySound("last2")
	}

	// 有人剩牌不超过 2 张时切到紧张版 BGM，否则保持普通版
	warning := false
	for _, p := range m.Game().State().Players {
		if p.CardsCount <= 2 {
			warning = true
			break
		}
	}
	if warning {
		// 紧张版 BGM 在两首之间随机选一首；已在播其中一首时保持不变，避免反复切歌
		m.PlayBGMAnyOf("bgm_warning", "bgm_warning2")
	} else {
		m.PlayBGM("bgm_normal")
	}
}

func handleMsgGameOver(m model.Model, msg *protocol.Message) tea.Cmd {
	var payload protocol.GameOverPayload
	_ = payloadconv.DecodePayload(msg.Type, msg.Payload, &payload)

	// 先存好结算数据，保持当前出牌画面展示最后一手牌，3 秒后再切结算页
	m.Game().State().Winner = payload.WinnerName
	m.Game().State().WinnerIsLandlord = payload.IsLandlord
	m.Game().State().FinalMultiplier = payload.Multiplier
	m.Game().State().Scores = payload.Scores

	if m.Game().State().IsLandlord == m.Game().State().WinnerIsLandlord {
		m.PlaySound("win")
	} else {
		m.PlaySound("lose")
	}
	m.StopBGM()

	return tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return model.GameOverDelayMsg{}
	})
}

// playCardVoice 用男声播报刚打出的牌：单/对/三张报点数，其余报牌型。
// 文件名与 internal/sound/sounds 下的英文命名一一对应。
func playCardVoice(m model.Model, handType string, cards []card.Card) {
	switch handType {
	case rule.Single.String():
		m.PlaySound("single_" + rankToken(cards))
	case rule.Pair.String():
		m.PlaySound("pair_" + rankToken(cards))
	case rule.Trio.String():
		m.PlaySound("trio_" + rankToken(cards))
	case rule.TrioWithSingle.String():
		m.PlaySound("type_trio_single")
	case rule.TrioWithPair.String():
		m.PlaySound("type_trio_pair")
	case rule.Straight.String():
		m.PlaySequence("type_straight", "straight")
	case rule.PairStraight.String():
		m.PlaySound("type_pairstraight")
	case rule.Plane.String(), rule.PlaneWithSingles.String(), rule.PlaneWithPairs.String():
		m.PlaySequence("type_plane", "plane")
	case rule.Bomb.String():
		m.PlaySequence("type_bomb", "bomb")
	case rule.FourWithTwo.String():
		m.PlaySound("type_four_two")
	case rule.FourWithTwoPairs.String():
		m.PlaySound("type_four_twopair")
	case rule.Rocket.String():
		m.PlaySequence("type_rocket", "bomb")
	}
}

// rankToken 返回报牌语音文件名中的点数部分（与 sounds 文件名一致）。
func rankToken(cards []card.Card) string {
	switch modeRank(cards) {
	case card.RankBlackJoker:
		return "joker_small"
	case card.RankRedJoker:
		return "joker_big"
	default:
		return modeRank(cards).String()
	}
}

// modeRank 返回出现次数最多的点数（即单/对/三张的主点数）。
func modeRank(cards []card.Card) card.Rank {
	counts := make(map[card.Rank]int)
	var best card.Rank
	bestN := 0
	for _, c := range cards {
		counts[c.Rank]++
		if counts[c.Rank] > bestN {
			bestN = counts[c.Rank]
			best = c.Rank
		}
	}
	return best
}
