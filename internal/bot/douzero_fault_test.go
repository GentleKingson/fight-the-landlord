package bot

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
)

func TestDouZeroFaultMatrix(t *testing.T) {
	gameContext := GameContext{
		Hand:       faultCards(card.Rank3, card.Rank4, card.Rank5),
		MustPlay:   true,
		CanBeat:    true,
		DouZeroPos: DouZeroPosLandlord,
	}
	heuristic := NewHeuristicEngine().DecidePlay(context.Background(), "fallback", gameContext)

	t.Run("normal response", func(t *testing.T) {
		service := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			assert.Equal(t, "/decide_play", request.URL.Path)
			assert.Equal(t, http.MethodPost, request.Method)
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"action":[4]}`))
		}))
		defer service.Close()

		got := NewDouZeroEngine(service.URL).DecidePlay(context.Background(), "normal", gameContext)
		require.Len(t, got, 1)
		assert.Equal(t, card.Rank4, got[0].Rank)
	})

	t.Run("timeout falls back", func(t *testing.T) {
		release := make(chan struct{})
		service := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			select {
			case <-request.Context().Done():
			case <-release:
			}
		}))
		defer service.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		got := NewDouZeroEngine(service.URL).DecidePlay(ctx, "timeout", gameContext)
		close(release)
		assertLegalFallback(t, heuristic, got)
	})

	t.Run("connection refusal falls back", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		endpoint := fmt.Sprintf("http://%s", listener.Addr())
		require.NoError(t, listener.Close())

		got := NewDouZeroEngine(endpoint).DecidePlay(context.Background(), "refused", gameContext)
		assertLegalFallback(t, heuristic, got)
	})

	for _, testCase := range []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"action":`},
		{name: "card absent from hand", body: `{"action":[30]}`},
		{name: "service-declared error", body: `{"error":"invalid model response"}`},
	} {
		t.Run(testCase.name+" falls back", func(t *testing.T) {
			service := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				_, _ = response.Write([]byte(testCase.body))
			}))
			defer service.Close()

			got := NewDouZeroEngine(service.URL).DecidePlay(context.Background(), "invalid", gameContext)
			assertLegalFallback(t, heuristic, got)
		})
	}
}

func faultCards(ranks ...card.Rank) []card.Card {
	result := make([]card.Card, len(ranks))
	for index, rank := range ranks {
		result[index] = card.Card{Rank: rank, Suit: card.Spade}
	}
	return result
}

func assertLegalFallback(t *testing.T, expected, actual []card.Card) {
	t.Helper()
	require.Equal(t, expected, actual)
	parsed, err := rule.ParseHand(actual)
	require.NoError(t, err)
	assert.NotEqual(t, rule.Invalid, parsed.Type)
}
