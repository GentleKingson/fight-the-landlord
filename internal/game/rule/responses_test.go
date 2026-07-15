package rule

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
)

func TestCanBeat_RocketDoesNotBeatRocket(t *testing.T) {
	t.Parallel()

	rocketCards := testRuleCards(card.RankBlackJoker, card.RankRedJoker)
	rocket, err := ParseHand(rocketCards)
	require.NoError(t, err)

	assert.False(t, CanBeat(rocket, rocket))
	assert.False(t, CanBeatWithHand(rocketCards, rocket))
	assert.Empty(t, ListLegalResponses(rocketCards, rocket))
}

func TestListLegalResponses_AllHandTypes(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		opponentRanks []card.Rank
		playerRanks   []card.Rank
		expectedType  HandType
	}{
		{"single", ranks(card.Rank3), ranks(card.Rank4), Single},
		{"pair", ranks(card.Rank3, card.Rank3), ranks(card.Rank4, card.Rank4), Pair},
		{"trio", ranks(card.Rank3, card.Rank3, card.Rank3), ranks(card.Rank4, card.Rank4, card.Rank4), Trio},
		{"trio with single", ranks(card.Rank3, card.Rank3, card.Rank3, card.RankA), ranks(card.Rank4, card.Rank4, card.Rank4, card.Rank5), TrioWithSingle},
		{"trio with pair", ranks(card.Rank3, card.Rank3, card.Rank3, card.Rank5, card.Rank5), ranks(card.Rank4, card.Rank4, card.Rank4, card.Rank6, card.Rank6), TrioWithPair},
		{"straight", ranks(card.Rank3, card.Rank4, card.Rank5, card.Rank6, card.Rank7), ranks(card.Rank4, card.Rank5, card.Rank6, card.Rank7, card.Rank8), Straight},
		{"pair straight", ranks(card.Rank3, card.Rank3, card.Rank4, card.Rank4, card.Rank5, card.Rank5), ranks(card.Rank4, card.Rank4, card.Rank5, card.Rank5, card.Rank6, card.Rank6), PairStraight},
		{"plane", ranks(card.Rank3, card.Rank3, card.Rank3, card.Rank4, card.Rank4, card.Rank4), ranks(card.Rank4, card.Rank4, card.Rank4, card.Rank5, card.Rank5, card.Rank5), Plane},
		{"plane with singles", ranks(card.Rank3, card.Rank3, card.Rank3, card.Rank4, card.Rank4, card.Rank4, card.Rank7, card.Rank8), ranks(card.Rank4, card.Rank4, card.Rank4, card.Rank5, card.Rank5, card.Rank5, card.Rank6, card.Rank7), PlaneWithSingles},
		{"plane with pairs", ranks(card.Rank3, card.Rank3, card.Rank3, card.Rank4, card.Rank4, card.Rank4, card.Rank7, card.Rank7, card.Rank8, card.Rank8), ranks(card.Rank4, card.Rank4, card.Rank4, card.Rank5, card.Rank5, card.Rank5, card.Rank6, card.Rank6, card.Rank7, card.Rank7), PlaneWithPairs},
		{"bomb", ranks(card.Rank3, card.Rank3, card.Rank3, card.Rank3), ranks(card.Rank4, card.Rank4, card.Rank4, card.Rank4), Bomb},
		{"four with two", ranks(card.Rank3, card.Rank3, card.Rank3, card.Rank3, card.Rank4, card.Rank5), ranks(card.Rank4, card.Rank4, card.Rank4, card.Rank4, card.Rank5, card.Rank6), FourWithTwo},
		{"four with two pairs", ranks(card.Rank3, card.Rank3, card.Rank3, card.Rank3, card.Rank4, card.Rank4, card.Rank5, card.Rank5), ranks(card.Rank4, card.Rank4, card.Rank4, card.Rank4, card.Rank5, card.Rank5, card.Rank6, card.Rank6), FourWithTwoPairs},
		{"rocket fallback", ranks(card.Rank2, card.Rank2), ranks(card.RankBlackJoker, card.RankRedJoker), Rocket},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			opponent, err := ParseHand(testRuleCards(testCase.opponentRanks...))
			require.NoError(t, err)
			playerHand := testRuleCards(testCase.playerRanks...)
			responses := ListLegalResponses(playerHand, opponent)

			require.NotEmpty(t, responses)
			assert.True(t, containsResponseType(responses, testCase.expectedType))
			assertAllGoResponsesAreLegal(t, responses, opponent)
			assert.True(t, CanBeatWithHand(playerHand, opponent))

			smallest := FindSmallestBeatingCards(playerHand, opponent)
			require.NotNil(t, smallest)
			parsedSmallest, err := ParseHand(smallest)
			require.NoError(t, err)
			assert.True(t, CanBeat(parsedSmallest, opponent))
			assert.Equal(t, testCase.expectedType, parsedSmallest.Type)
		})
	}
}

func TestListLegalResponses_StrictWingSemantics(t *testing.T) {
	t.Parallel()

	t.Run("paired single wings are not a plane with singles", func(t *testing.T) {
		t.Parallel()

		opponent := mustParseRuleHand(t, ranks(
			card.Rank3, card.Rank3, card.Rank3,
			card.Rank4, card.Rank4, card.Rank4,
			card.Rank7, card.Rank8,
		))
		playerHand := testRuleCards(
			card.Rank4, card.Rank4, card.Rank4,
			card.Rank5, card.Rank5, card.Rank5,
			card.Rank6, card.Rank6,
		)

		assert.Empty(t, ListLegalResponses(playerHand, opponent))
		assert.False(t, CanBeatWithHand(playerHand, opponent))
		assert.Nil(t, FindSmallestBeatingCards(playerHand, opponent))
	})

	t.Run("another bomb cannot be split into two pair wings", func(t *testing.T) {
		t.Parallel()

		opponent := mustParseRuleHand(t, ranks(
			card.Rank3, card.Rank3, card.Rank3, card.Rank3,
			card.Rank4, card.Rank4, card.Rank5, card.Rank5,
		))
		playerHand := testRuleCards(
			card.Rank4, card.Rank4, card.Rank4, card.Rank4,
			card.Rank5, card.Rank5, card.Rank5, card.Rank5,
		)

		responses := ListLegalResponses(playerHand, opponent)
		require.NotEmpty(t, responses)
		assert.False(t, containsResponseType(responses, FourWithTwoPairs))
		assertAllGoResponsesAreLegal(t, responses, opponent)
	})

	t.Run("a single kicker may be split from another trio", func(t *testing.T) {
		t.Parallel()

		opponent := mustParseRuleHand(t, ranks(card.Rank3, card.Rank3, card.Rank3, card.RankA))
		playerHand := testRuleCards(
			card.Rank4, card.Rank4, card.Rank4,
			card.Rank5, card.Rank5, card.Rank5,
		)

		smallest := FindSmallestBeatingCards(playerHand, opponent)
		parsedSmallest, err := ParseHand(smallest)
		require.NoError(t, err)
		assert.Equal(t, TrioWithSingle, parsedSmallest.Type)
		assert.Equal(t, card.Rank4, parsedSmallest.KeyRank)
	})
}

func TestListLegalResponses_OrderingAndValidation(t *testing.T) {
	t.Parallel()

	opponent := mustParseRuleHand(t, ranks(card.Rank6, card.Rank6))
	playerHand := testRuleCards(
		card.Rank7, card.Rank7,
		card.Rank3, card.Rank3, card.Rank3, card.Rank3,
		card.RankBlackJoker, card.RankRedJoker,
	)
	responses := ListLegalResponses(playerHand, opponent)

	require.Len(t, responses, 3)
	expectedTypes := []HandType{Pair, Bomb, Rocket}
	for i, response := range responses {
		parsedResponse, err := ParseHand(response)
		require.NoError(t, err)
		assert.Equal(t, expectedTypes[i], parsedResponse.Type)
	}
	assertAllGoResponsesAreLegal(t, responses, opponent)
}

func TestListLegalResponses_UniqueRankMultisets(t *testing.T) {
	t.Parallel()

	opponent := mustParseRuleHand(t, ranks(card.Rank7, card.Rank7, card.Rank7, card.Rank8, card.Rank8))
	playerHand := testRuleCards(
		card.Rank3, card.Rank3, card.Rank3, card.Rank3,
		card.Rank4, card.Rank4,
		card.Rank5, card.Rank5,
		card.Rank6, card.Rank6,
		card.Rank8, card.Rank8, card.Rank8,
		card.Rank9, card.Rank9,
		card.Rank10, card.RankJ, card.Rank2,
		card.RankBlackJoker, card.RankRedJoker,
	)
	responses := ListLegalResponses(playerHand, opponent)

	require.NotEmpty(t, responses)
	seen := make(map[string]struct{}, len(responses))
	for _, response := range responses {
		signature := responseRankSignature(response)
		_, duplicate := seen[signature]
		assert.False(t, duplicate, "duplicate logical response %s", signature)
		seen[signature] = struct{}{}
	}
	assertAllGoResponsesAreLegal(t, responses, opponent)
}

func TestFindSmallestBeatingCards_LeadDoesNotDependOnSortOrder(t *testing.T) {
	t.Parallel()

	playerHand := testRuleCards(card.RankA, card.Rank3, card.Rank10, card.Rank5)
	result := FindSmallestBeatingCards(playerHand, ParsedHand{})

	require.Len(t, result, 1)
	assert.Equal(t, card.Rank3, result[0].Rank)
}

func TestCanBeatWithHand_EmptyHandHasNoLegalLead(t *testing.T) {
	t.Parallel()

	assert.False(t, CanBeatWithHand(nil, ParsedHand{}))
	assert.Empty(t, ListLegalResponses(nil, ParsedHand{}))
	assert.Nil(t, FindSmallestBeatingCards(nil, ParsedHand{}))
}

func ranks(values ...card.Rank) []card.Rank {
	return values
}

func mustParseRuleHand(t *testing.T, handRanks []card.Rank) ParsedHand {
	t.Helper()
	parsedHand, err := ParseHand(testRuleCards(handRanks...))
	require.NoError(t, err)
	return parsedHand
}

func containsResponseType(responses [][]card.Card, expectedType HandType) bool {
	for _, response := range responses {
		parsedResponse, err := ParseHand(response)
		if err == nil && parsedResponse.Type == expectedType {
			return true
		}
	}
	return false
}

func assertAllGoResponsesAreLegal(t *testing.T, responses [][]card.Card, opponent ParsedHand) {
	t.Helper()
	for _, response := range responses {
		parsedResponse, err := ParseHand(response)
		require.NoError(t, err)
		assert.True(t, CanBeat(parsedResponse, opponent), "illegal response: %v", response)
	}
}

func responseRankSignature(response []card.Card) string {
	ranks := make([]card.Rank, len(response))
	for i, responseCard := range response {
		ranks[i] = responseCard.Rank
	}
	slices.Sort(ranks)
	return fmt.Sprint(ranks)
}
