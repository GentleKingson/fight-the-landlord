package rule

import "github.com/palemoky/fight-the-landlord/internal/game/card"

// FindSmallestBeatingCards 找到能打过 opponentHand 的最小牌组
// 如果找不到，返回 nil
func FindSmallestBeatingCards(playerHand []card.Card, opponentHand ParsedHand) []card.Card {
	if opponentHand.Type == Rocket {
		return nil
	}
	// 如果是新一轮，出最小的单牌
	if opponentHand.IsEmpty() {
		if len(playerHand) > 0 {
			return []card.Card{playerHand[len(playerHand)-1]}
		}
		return nil
	}

	analysis := analyzeCards(playerHand)

	// 优先尝试找同类型的最小牌
	var result []card.Card

	switch opponentHand.Type {
	case Single:
		result = findSmallestBeatingSingle(playerHand, analysis, opponentHand)
	case Pair:
		result = findSmallestBeatingPair(playerHand, analysis, opponentHand)
	case Trio:
		result = findSmallestBeatingTrio(playerHand, analysis, opponentHand, 0)
	case TrioWithSingle:
		result = findSmallestBeatingTrio(playerHand, analysis, opponentHand, 1)
	case TrioWithPair:
		result = findSmallestBeatingTrio(playerHand, analysis, opponentHand, 2)
	case Straight, PairStraight, Plane, PlaneWithSingles, PlaneWithPairs, FourWithTwo, FourWithTwoPairs:
		result = findSmallestBeatingGroup(playerHand, analysis, opponentHand)
	}

	// 如果找到了同类型的牌，返回
	if result != nil {
		return result
	}

	// 否则尝试用最小的炸弹
	result = findSmallestBomb(playerHand, analysis, opponentHand)
	if result != nil {
		return result
	}

	// 最后尝试王炸（一般不会用）
	if hasRocket(analysis) && opponentHand.Type != Rocket {
		return findRocket(playerHand)
	}

	return nil
}

// findFirstBeating 从多个点数列表中找第一个能打过的牌
func findFirstBeating(playerHand []card.Card, rankLists [][]card.Rank, keyRank card.Rank, count int) []card.Card {
	for _, ranks := range rankLists {
		for _, r := range ranks {
			if r > keyRank {
				return findCardsWithRank(playerHand, r, count)
			}
		}
	}
	return nil
}

// findSmallestBeatingSingle 找到能打过的最小单牌
func findSmallestBeatingSingle(playerHand []card.Card, analysis HandAnalysis, opponentHand ParsedHand) []card.Card {
	return findFirstBeating(playerHand,
		[][]card.Rank{analysis.ones, analysis.pairs, analysis.trios, analysis.fours},
		opponentHand.KeyRank, 1)
}

// findSmallestBeatingPair 找到能打过的最小对子
func findSmallestBeatingPair(playerHand []card.Card, analysis HandAnalysis, opponentHand ParsedHand) []card.Card {
	return findFirstBeating(playerHand,
		[][]card.Rank{analysis.pairs, analysis.trios, analysis.fours},
		opponentHand.KeyRank, 2)
}

// findSmallestBeatingTrio 找到能打过的最小三张（带或不带）
func findSmallestBeatingTrio(playerHand []card.Card, analysis HandAnalysis, opponentHand ParsedHand, kickerType int) []card.Card {
	for _, ranks := range [][]card.Rank{analysis.trios, analysis.fours} {
		for _, r := range ranks {
			if r > opponentHand.KeyRank {
				result := findCardsWithRank(playerHand, r, 3)
				if kickerType == 0 {
					return result
				}
				if kickers := findSmallestKickers(playerHand, analysis, r, kickerType); kickers != nil {
					return append(result, kickers...)
				}
			}
		}
	}
	return nil
}

// findSmallestBomb 找到最小的炸弹
func findSmallestBomb(playerHand []card.Card, analysis HandAnalysis, opponentHand ParsedHand) []card.Card {
	if opponentHand.Type == Rocket {
		return nil
	}
	for _, r := range analysis.fours {
		if opponentHand.Type != Bomb || r > opponentHand.KeyRank {
			return findCardsWithRank(playerHand, r, 4)
		}
	}
	return nil
}

// findSmallestKickers 找到最小的带牌
// kickerType: 1=带单张, 2=带对子
func findSmallestKickers(playerHand []card.Card, analysis HandAnalysis, excludeRank card.Rank, kickerType int) []card.Card {
	var kickers []card.Card
	neededCards := kickerType // 1张单牌或2张(1对)

	// collectFromRanks 从给定的点数列表中收集 kicker 牌
	collectFromRanks := func(ranks []card.Rank, countPerRank int) bool {
		for _, r := range ranks {
			if r != excludeRank {
				kickers = append(kickers, findCardsWithRank(playerHand, r, countPerRank)...)
				if len(kickers) >= neededCards {
					kickers = kickers[:neededCards]
					return true
				}
			}
		}
		return false
	}

	if kickerType == 1 {
		// 带单张：优先单牌、对子，必要时也能拆三张或炸弹。
		if collectFromRanks(analysis.ones, 1) || collectFromRanks(analysis.pairs, 1) ||
			collectFromRanks(analysis.trios, 1) || collectFromRanks(analysis.fours, 1) {
			return kickers
		}
	} else {
		// 带对子：从对子、三张、四张中取
		if collectFromRanks(analysis.pairs, 2) ||
			collectFromRanks(analysis.trios, 2) ||
			collectFromRanks(analysis.fours, 2) {
			return kickers
		}
	}
	return nil
}

// findSmallestBeatingGroup 按主体点数递增构造顺牌、飞机和四带二。
// 与 ParseHand 共用最终判型，机器人与超时出牌只返回服务端接受的动作。
func findSmallestBeatingGroup(hand []card.Card, analysis HandAnalysis, target ParsedHand) []card.Card {
	length, repeat, wings, wingSize := groupShape(target)
	if length < 1 {
		return nil
	}
	maxRank := card.RankA
	if repeat == 4 {
		maxRank = card.Rank2
	}
	for start := max(card.Rank3, target.KeyRank+1); start+card.Rank(length)-1 <= maxRank; start++ {
		end := start + card.Rank(length) - 1
		var candidate []card.Card
		for rank := start; rank <= end; rank++ {
			if analysis.counts[rank] < repeat {
				break
			}
			candidate = append(candidate, findCardsWithRank(hand, rank, repeat)...)
		}
		if len(candidate) != length*repeat {
			continue
		}
		kickers := findGroupKickers(hand, analysis, start, end, wings, wingSize)
		if len(kickers) != wings*wingSize {
			continue
		}
		candidate = append(candidate, kickers...)
		parsed, err := ParseHand(candidate)
		if err == nil && CanBeat(parsed, target) {
			return candidate
		}
	}
	return nil
}

// groupShape 描述顺牌或四带二的主体长度、每点张数、带牌组数和每组张数。
func groupShape(target ParsedHand) (length, repeat, wings, wingSize int) {
	length, repeat, wings, wingSize = target.Length, 1, 0, 1
	switch target.Type {
	case PairStraight:
		repeat = 2
	case Plane:
		repeat = 3
	case PlaneWithSingles:
		repeat, wings = 3, length
	case PlaneWithPairs:
		repeat, wings, wingSize = 3, length, 2
	case FourWithTwo:
		length, repeat, wings = 1, 4, 2
	case FourWithTwoPairs:
		length, repeat, wings, wingSize = 1, 4, 2, 2
	}
	return length, repeat, wings, wingSize
}

// findGroupKickers 从主体以外的点数取带牌；单翅每个点数最多两张，对翅每个点数一对。
func findGroupKickers(hand []card.Card, analysis HandAnalysis, start, end card.Rank, remaining, wingSize int) []card.Card {
	var kickers []card.Card
	for rank := card.Rank3; rank <= card.RankRedJoker && remaining > 0; rank++ {
		if rank >= start && rank <= end {
			continue
		}
		count := analysis.counts[rank]
		if wingSize == 2 {
			if count >= 2 {
				kickers = append(kickers, findCardsWithRank(hand, rank, 2)...)
				remaining--
			}
		} else {
			count = min(count, 2, remaining)
			if count > 0 {
				kickers = append(kickers, findCardsWithRank(hand, rank, count)...)
				remaining -= count
			}
		}
	}
	return kickers
}

// findCardsWithRank 从手牌中找到指定点数的牌
func findCardsWithRank(playerHand []card.Card, rank card.Rank, count int) []card.Card {
	var result []card.Card
	for _, c := range playerHand {
		if c.Rank == rank {
			result = append(result, c)
			if len(result) >= count {
				return result
			}
		}
	}
	return result
}

// hasRocket 检查是否有王炸
func hasRocket(analysis HandAnalysis) bool {
	return analysis.counts[card.RankBlackJoker] > 0 && analysis.counts[card.RankRedJoker] > 0
}

// findRocket 找到王炸
func findRocket(playerHand []card.Card) []card.Card {
	var result []card.Card
	for _, c := range playerHand {
		if c.Rank == card.RankBlackJoker || c.Rank == card.RankRedJoker {
			result = append(result, c)
		}
	}
	return result
}
