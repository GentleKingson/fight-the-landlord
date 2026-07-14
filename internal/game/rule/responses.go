package rule

import (
	"cmp"
	"slices"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
)

type responseRankGroup struct {
	rank  card.Rank
	cards []card.Card
}

type responseCandidate struct {
	cards      []card.Card
	parsed     ParsedHand
	rankVector []card.Rank
}

// ListLegalResponses returns every legal logical response in deterministic
// order. Suits do not affect hand strength, so equivalent suit-only variants
// of the same rank multiset are represented by one concrete card selection.
func ListLegalResponses(playerHand []card.Card, opponentHand ParsedHand) [][]card.Card {
	if len(playerHand) == 0 {
		return nil
	}

	isLead := opponentHand.IsEmpty()
	if !isLead && opponentHand.Type == Rocket {
		return nil
	}

	groups := groupResponseCardsByRank(playerHand)
	selectedCounts := make([]int, len(groups))
	allowedLengths := responseLengths(opponentHand, isLead)
	maxLength := len(playerHand)
	if !isLead {
		maxLength = largestResponseLength(allowedLengths)
	}
	suffixCardCounts := responseSuffixCardCounts(groups)

	responses := make([]responseCandidate, 0)
	var visit func(groupIndex, selectedTotal int)
	visit = func(groupIndex, selectedTotal int) {
		if selectedTotal > maxLength {
			return
		}
		if !isLead && !canReachResponseLength(allowedLengths, selectedTotal, suffixCardCounts[groupIndex]) {
			return
		}

		if groupIndex == len(groups) {
			if selectedTotal == 0 {
				return
			}
			if !isLead {
				if _, ok := allowedLengths[selectedTotal]; !ok {
					return
				}
			}

			candidateCards, rankVector := materializeResponse(groups, selectedCounts)
			parsedHand, err := ParseHand(candidateCards)
			if err != nil {
				return
			}
			if !isLead && !CanBeat(parsedHand, opponentHand) {
				return
			}
			responses = append(responses, responseCandidate{
				cards:      candidateCards,
				parsed:     parsedHand,
				rankVector: rankVector,
			})
			return
		}

		available := len(groups[groupIndex].cards)
		for count := 0; count <= available && selectedTotal+count <= maxLength; count++ {
			selectedCounts[groupIndex] = count
			visit(groupIndex+1, selectedTotal+count)
		}
		selectedCounts[groupIndex] = 0
	}

	visit(0, 0)
	slices.SortFunc(responses, func(left, right responseCandidate) int {
		return compareResponseCandidates(left, right, opponentHand, isLead)
	})

	result := make([][]card.Card, len(responses))
	for i, response := range responses {
		result[i] = response.cards
	}
	return result
}

func groupResponseCardsByRank(playerHand []card.Card) []responseRankGroup {
	cardsByRank := make(map[card.Rank][]card.Card)
	for _, currentCard := range playerHand {
		cardsByRank[currentCard.Rank] = append(cardsByRank[currentCard.Rank], currentCard)
	}

	ranks := make([]card.Rank, 0, len(cardsByRank))
	for rank := range cardsByRank {
		ranks = append(ranks, rank)
	}
	slices.Sort(ranks)

	groups := make([]responseRankGroup, 0, len(ranks))
	for _, rank := range ranks {
		cards := cardsByRank[rank]
		slices.SortFunc(cards, func(left, right card.Card) int {
			if suitComparison := cmp.Compare(left.Suit, right.Suit); suitComparison != 0 {
				return suitComparison
			}
			return cmp.Compare(left.Color, right.Color)
		})
		groups = append(groups, responseRankGroup{rank: rank, cards: cards})
	}
	return groups
}

func responseLengths(opponentHand ParsedHand, isLead bool) map[int]struct{} {
	if isLead {
		return nil
	}

	lengths := map[int]struct{}{
		2: {},
		4: {},
	}
	if handLength := parsedHandCardCount(opponentHand); handLength > 0 {
		lengths[handLength] = struct{}{}
	}
	return lengths
}

func parsedHandCardCount(hand ParsedHand) int {
	if len(hand.Cards) > 0 {
		return len(hand.Cards)
	}

	switch hand.Type {
	case Single:
		return 1
	case Pair, Rocket:
		return 2
	case Trio:
		return 3
	case TrioWithSingle, Bomb:
		return 4
	case TrioWithPair:
		return 5
	case FourWithTwo:
		return 6
	case FourWithTwoPairs:
		return 8
	case Straight:
		return hand.Length
	case PairStraight:
		return hand.Length * 2
	case Plane:
		return hand.Length * 3
	case PlaneWithSingles:
		return hand.Length * 4
	case PlaneWithPairs:
		return hand.Length * 5
	default:
		return 0
	}
}

func largestResponseLength(lengths map[int]struct{}) int {
	largest := 0
	for length := range lengths {
		if length > largest {
			largest = length
		}
	}
	return largest
}

func responseSuffixCardCounts(groups []responseRankGroup) []int {
	suffixCounts := make([]int, len(groups)+1)
	for i := len(groups) - 1; i >= 0; i-- {
		suffixCounts[i] = suffixCounts[i+1] + len(groups[i].cards)
	}
	return suffixCounts
}

func canReachResponseLength(lengths map[int]struct{}, selectedTotal, remainingCards int) bool {
	for length := range lengths {
		if length >= selectedTotal && length <= selectedTotal+remainingCards {
			return true
		}
	}
	return false
}

func materializeResponse(groups []responseRankGroup, selectedCounts []int) ([]card.Card, []card.Rank) {
	cards := make([]card.Card, 0)
	rankVector := make([]card.Rank, 0)
	for i, group := range groups {
		cards = append(cards, group.cards[:selectedCounts[i]]...)
		for range selectedCounts[i] {
			rankVector = append(rankVector, group.rank)
		}
	}
	return cards, rankVector
}

func compareResponseCandidates(left, right responseCandidate, opponentHand ParsedHand, isLead bool) int {
	if priorityComparison := cmp.Compare(
		responsePriority(left.parsed, opponentHand, isLead),
		responsePriority(right.parsed, opponentHand, isLead),
	); priorityComparison != 0 {
		return priorityComparison
	}
	if rankComparison := cmp.Compare(left.parsed.KeyRank, right.parsed.KeyRank); rankComparison != 0 {
		return rankComparison
	}
	if lengthComparison := cmp.Compare(len(left.cards), len(right.cards)); lengthComparison != 0 {
		return lengthComparison
	}
	for i := range left.rankVector {
		if rankComparison := cmp.Compare(left.rankVector[i], right.rankVector[i]); rankComparison != 0 {
			return rankComparison
		}
	}
	return 0
}

func responsePriority(hand, opponentHand ParsedHand, isLead bool) int {
	if !isLead {
		switch {
		case hand.Type == opponentHand.Type:
			return 0
		case hand.Type == Bomb:
			return 1
		case hand.Type == Rocket:
			return 2
		default:
			return 3
		}
	}

	switch hand.Type {
	case Single:
		return 0
	case Pair:
		return 1
	case Trio:
		return 2
	case TrioWithSingle:
		return 3
	case TrioWithPair:
		return 4
	case Straight:
		return 5
	case PairStraight:
		return 6
	case Plane:
		return 7
	case PlaneWithSingles:
		return 8
	case PlaneWithPairs:
		return 9
	case FourWithTwo:
		return 10
	case FourWithTwoPairs:
		return 11
	case Bomb:
		return 12
	case Rocket:
		return 13
	default:
		return 14
	}
}
