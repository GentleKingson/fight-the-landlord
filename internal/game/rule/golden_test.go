package rule

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
)

type ruleGoldenFixture struct {
	Version         int                    `json:"version"`
	ParseCases      []ruleGoldenParseCase  `json:"parse_cases"`
	ComparisonCases []ruleGoldenComparison `json:"comparison_cases"`
	ResponseCases   []ruleGoldenResponse   `json:"response_cases"`
}

type ruleGoldenParseCase struct {
	Name     string                  `json:"name"`
	Ranks    []int                   `json:"ranks"`
	Expected *ruleGoldenParsedResult `json:"expected"`
}

type ruleGoldenParsedResult struct {
	Type    string `json:"type"`
	KeyRank int    `json:"key_rank"`
	Length  int    `json:"length"`
}

type ruleGoldenComparison struct {
	Name      string `json:"name"`
	Candidate []int  `json:"candidate"`
	Previous  []int  `json:"previous"`
	CanBeat   bool   `json:"can_beat"`
}

type ruleGoldenResponse struct {
	Name      string  `json:"name"`
	Hand      []int   `json:"hand"`
	Previous  []int   `json:"previous"`
	Responses [][]int `json:"responses"`
	Smallest  []int   `json:"smallest"`
}

func TestRuleGoldenFixture(t *testing.T) {
	fixture := loadRuleGoldenFixture(t)
	require.Equal(t, 1, fixture.Version)

	t.Run("parse", func(t *testing.T) {
		for _, testCase := range fixture.ParseCases {
			t.Run(testCase.Name, func(t *testing.T) {
				parsed, err := ParseHand(goldenCards(testCase.Ranks))
				if testCase.Expected == nil {
					assert.Error(t, err)
					return
				}

				require.NoError(t, err)
				assert.Equal(t, testCase.Expected.Type, goldenHandTypeName(parsed.Type))
				assert.Equal(t, testCase.Expected.KeyRank, int(parsed.KeyRank))
				assert.Equal(t, testCase.Expected.Length, parsed.Length)
			})
		}
	})

	t.Run("comparison", func(t *testing.T) {
		for _, testCase := range fixture.ComparisonCases {
			t.Run(testCase.Name, func(t *testing.T) {
				candidate, err := ParseHand(goldenCards(testCase.Candidate))
				require.NoError(t, err)
				previous, err := ParseHand(goldenCards(testCase.Previous))
				require.NoError(t, err)
				assert.Equal(t, testCase.CanBeat, CanBeat(candidate, previous))
			})
		}
	})

	t.Run("responses", func(t *testing.T) {
		for _, testCase := range fixture.ResponseCases {
			t.Run(testCase.Name, func(t *testing.T) {
				previous := ParsedHand{}
				if len(testCase.Previous) > 0 {
					var err error
					previous, err = ParseHand(goldenCards(testCase.Previous))
					require.NoError(t, err)
				}

				hand := goldenCards(testCase.Hand)
				responses := ListLegalResponses(hand, previous)
				assert.Equal(t, testCase.Responses, goldenResponseRanks(responses))

				smallest := FindSmallestBeatingCards(hand, previous)
				if testCase.Smallest == nil {
					assert.Nil(t, smallest)
				} else {
					assert.Equal(t, testCase.Smallest, goldenRanks(smallest))
				}
			})
		}
	})
}

func loadRuleGoldenFixture(t *testing.T) ruleGoldenFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/rule_golden.json")
	require.NoError(t, err)

	var fixture ruleGoldenFixture
	require.NoError(t, json.Unmarshal(data, &fixture))
	return fixture
}

func goldenCards(ranks []int) []card.Card {
	occurrences := make(map[int]int)
	cards := make([]card.Card, len(ranks))
	for index, rank := range ranks {
		suit := occurrences[rank] % 4
		occurrences[rank]++
		if rank >= int(card.RankBlackJoker) {
			suit = int(card.Joker)
		}
		cards[index] = card.Card{
			Rank:  card.Rank(rank),
			Suit:  card.Suit(suit),
			Color: card.CardColor(suit % 2),
		}
	}
	return cards
}

func goldenResponseRanks(responses [][]card.Card) [][]int {
	ranks := make([][]int, len(responses))
	for index, response := range responses {
		ranks[index] = goldenRanks(response)
	}
	return ranks
}

func goldenRanks(cards []card.Card) []int {
	ranks := make([]int, len(cards))
	for index, currentCard := range cards {
		ranks[index] = int(currentCard.Rank)
	}
	slices.Sort(ranks)
	return ranks
}

func goldenHandTypeName(handType HandType) string {
	return map[HandType]string{
		Single:           "single",
		Pair:             "pair",
		Trio:             "trio",
		TrioWithSingle:   "trio_with_single",
		TrioWithPair:     "trio_with_pair",
		Straight:         "straight",
		PairStraight:     "pair_straight",
		Plane:            "plane",
		PlaneWithSingles: "plane_with_singles",
		PlaneWithPairs:   "plane_with_pairs",
		Bomb:             "bomb",
		FourWithTwo:      "four_with_two",
		FourWithTwoPairs: "four_with_two_pairs",
		Rocket:           "rocket",
	}[handType]
}
