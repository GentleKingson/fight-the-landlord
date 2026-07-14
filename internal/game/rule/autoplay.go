package rule

import (
	"slices"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
)

// FindSmallestBeatingCards returns the weakest legal response. A same-type
// response is preferred over a bomb, and a bomb is preferred over a rocket.
func FindSmallestBeatingCards(playerHand []card.Card, opponentHand ParsedHand) []card.Card {
	if len(playerHand) == 0 {
		return nil
	}

	// Avoid enumerating every possible opening play: the weakest legal lead is
	// always the lowest single card.
	if opponentHand.IsEmpty() {
		smallest := playerHand[0]
		for _, currentCard := range playerHand[1:] {
			if cardLess(currentCard, smallest) {
				smallest = currentCard
			}
		}
		candidate := []card.Card{smallest}
		if _, err := ParseHand(candidate); err != nil {
			return nil
		}
		return candidate
	}

	responses := ListLegalResponses(playerHand, opponentHand)
	if len(responses) == 0 {
		return nil
	}
	return slices.Clone(responses[0])
}

func cardLess(left, right card.Card) bool {
	if left.Rank != right.Rank {
		return left.Rank < right.Rank
	}
	if left.Suit != right.Suit {
		return left.Suit < right.Suit
	}
	return left.Color < right.Color
}
