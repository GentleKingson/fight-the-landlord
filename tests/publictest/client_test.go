package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
)

func TestLegalCardsUsesRuleEngineForLeadAndResponse(t *testing.T) {
	t.Parallel()

	hand := []card.Card{
		{Suit: card.Spade, Rank: card.Rank3, Color: card.Black},
		{Suit: card.Heart, Rank: card.Rank5, Color: card.Red},
	}
	lead := legalCards(clientState{hand: hand, mustPlay: true})
	require.Len(t, lead, 1)
	assert.Equal(t, card.Rank3, lead[0].Rank)

	previous, err := rule.ParseHand([]card.Card{{Suit: card.Club, Rank: card.Rank4, Color: card.Black}})
	require.NoError(t, err)
	response := legalCards(clientState{hand: hand, lastPlayed: previous})
	require.Len(t, response, 1)
	assert.Equal(t, card.Rank5, response[0].Rank)
}

func TestLegalCardsReturnsPassWhenNothingCanBeat(t *testing.T) {
	t.Parallel()

	previous, err := rule.ParseHand([]card.Card{{Suit: card.Joker, Rank: card.RankRedJoker, Color: card.Red}})
	require.NoError(t, err)
	response := legalCards(clientState{
		hand:       []card.Card{{Suit: card.Spade, Rank: card.Rank3, Color: card.Black}},
		lastPlayed: previous,
	})
	assert.Empty(t, response)
}

func TestRemoveCardsUsesPhysicalCardIdentity(t *testing.T) {
	t.Parallel()

	hand := []card.Card{
		{Suit: card.Spade, Rank: card.Rank3, Color: card.Black},
		{Suit: card.Heart, Rank: card.Rank3, Color: card.Red},
	}
	remaining := removeCards(hand, hand[:1])
	require.Len(t, remaining, 1)
	assert.Equal(t, card.Heart, remaining[0].Suit)
}
