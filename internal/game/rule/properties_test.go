package rule

import (
	"math/rand"
	"testing"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
)

func TestLegalResponseProperties(t *testing.T) {
	t.Parallel()

	for seed := int64(0); seed < 400; seed++ {
		rng := rand.New(rand.NewSource(seed))
		deck := card.NewDeck()
		rng.Shuffle(len(deck), func(left, right int) { deck[left], deck[right] = deck[right], deck[left] })
		handSize := 1 + rng.Intn(12)
		playerHand := append([]card.Card(nil), deck[:handSize]...)

		var opponent ParsedHand
		for attempts := 0; attempts < 12 && opponent.IsEmpty(); attempts++ {
			start := handSize + rng.Intn(len(deck)-handSize)
			length := 1 + rng.Intn(8)
			candidate := make([]card.Card, 0, length)
			for index := 0; index < length; index++ {
				candidate = append(candidate, deck[(start+index)%len(deck)])
			}
			if parsed, err := ParseHand(candidate); err == nil {
				opponent = parsed
			}
		}
		assertResponseProperties(t, playerHand, opponent)
	}
}

func FuzzLegalResponseProperties(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5}, []byte{6})
	f.Add([]byte{0, 13, 26, 39, 52, 53}, []byte{1, 14})
	f.Add([]byte{}, []byte{})

	f.Fuzz(func(t *testing.T, handData, opponentData []byte) {
		playerHand := cardsFromFuzzBytes(handData, 12)
		if len(playerHand) == 0 {
			return
		}
		var opponent ParsedHand
		if opponentCards := cardsFromFuzzBytes(opponentData, 8); len(opponentCards) > 0 {
			parsed, err := ParseHand(opponentCards)
			if err != nil {
				return
			}
			opponent = parsed
		}
		assertResponseProperties(t, playerHand, opponent)
	})
}

// FuzzParseHand keeps arbitrary physical-card selections at the rule parser
// boundary. The deck mapping guarantees realistic card multiplicity while the
// cap prevents pathological allocations in combination recognition.
func FuzzParseHand(f *testing.F) {
	f.Add([]byte{0})
	f.Add([]byte{0, 13})
	f.Add([]byte{0, 1, 2, 3})
	f.Add([]byte{52, 53})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		cards := cardsFromFuzzBytes(data, 20)
		parsed, err := ParseHand(cards)
		if err != nil {
			return
		}
		if len(cards) == 0 {
			t.Fatal("empty hand was accepted")
		}
		if len(parsed.Cards) != len(cards) {
			t.Fatalf("parsed hand changed card count: got %d want %d", len(parsed.Cards), len(cards))
		}
	})
}

func TestRocketAndCategoryOrderingProperties(t *testing.T) {
	t.Parallel()

	rocket, err := ParseHand([]card.Card{
		{Suit: card.Joker, Rank: card.RankBlackJoker, Color: card.Black},
		{Suit: card.Joker, Rank: card.RankRedJoker, Color: card.Red},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidateCards := range [][]card.Card{
		{{Suit: card.Spade, Rank: card.Rank3, Color: card.Black}},
		{{Suit: card.Spade, Rank: card.Rank2, Color: card.Black}},
		{
			{Suit: card.Spade, Rank: card.Rank2, Color: card.Black},
			{Suit: card.Heart, Rank: card.Rank2, Color: card.Red},
			{Suit: card.Club, Rank: card.Rank2, Color: card.Black},
			{Suit: card.Diamond, Rank: card.Rank2, Color: card.Red},
		},
	} {
		candidate, parseErr := ParseHand(candidateCards)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if CanBeat(candidate, rocket) {
			t.Fatalf("%s unexpectedly beat rocket", candidate.Type)
		}
	}

	single, _ := ParseHand([]card.Card{{Suit: card.Spade, Rank: card.RankA, Color: card.Black}})
	pair, _ := ParseHand([]card.Card{
		{Suit: card.Spade, Rank: card.Rank3, Color: card.Black},
		{Suit: card.Heart, Rank: card.Rank3, Color: card.Red},
	})
	if CanBeat(single, pair) || CanBeat(pair, single) {
		t.Fatal("non-bomb hands of different categories must not beat each other")
	}
}

func assertResponseProperties(t *testing.T, playerHand []card.Card, opponent ParsedHand) {
	t.Helper()
	responses := ListLegalResponses(playerHand, opponent)
	for _, response := range responses {
		remaining, subset := remainingCardCount(playerHand, response)
		if !subset {
			t.Fatalf("response is not a subset of the player's hand: %#v", response)
		}
		parsed, err := ParseHand(response)
		if err != nil {
			t.Fatalf("response is not a legal hand: %v", err)
		}
		if !opponent.IsEmpty() && !CanBeat(parsed, opponent) {
			t.Fatalf("response %v does not beat %v", parsed, opponent)
		}
		if remaining+len(response) != len(playerHand) {
			t.Fatal("card count was not conserved")
		}
	}

	hint := FindSmallestBeatingCards(playerHand, opponent)
	if hint == nil {
		if len(responses) != 0 {
			t.Fatal("Hint returned nil despite legal responses")
		}
		return
	}
	if !isCardSubset(hint, playerHand) {
		t.Fatal("Hint is not a subset of the player's hand")
	}
	parsed, err := ParseHand(hint)
	if err != nil {
		t.Fatalf("Hint returned an illegal hand: %v", err)
	}
	if !opponent.IsEmpty() && !CanBeat(parsed, opponent) {
		t.Fatal("Hint did not beat the opponent hand")
	}
}

func cardsFromFuzzBytes(data []byte, limit int) []card.Card {
	deck := card.NewDeck()
	seen := make([]bool, len(deck))
	result := make([]card.Card, 0, min(len(data), limit))
	for _, value := range data {
		index := int(value) % len(deck)
		if seen[index] {
			continue
		}
		seen[index] = true
		result = append(result, deck[index])
		if len(result) == limit {
			break
		}
	}
	return result
}

func isCardSubset(candidate, hand []card.Card) bool {
	_, ok := remainingCardCount(hand, candidate)
	return ok
}

func remainingCardCount(hand, played []card.Card) (int, bool) {
	counts := make(map[card.Card]int, len(hand))
	for _, currentCard := range hand {
		counts[currentCard]++
	}
	for _, currentCard := range played {
		if counts[currentCard] == 0 {
			return 0, false
		}
		counts[currentCard]--
	}
	return len(hand) - len(played), true
}
