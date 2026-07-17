package bot

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/palemoky/fight-the-landlord/internal/observability"
)

func TestDouZeroFaultMatrix(t *testing.T) {
	leadContext := GameContext{
		Hand:        faultCards(card.Rank3, card.Rank4, card.Rank5),
		RecentPlays: [2]PlayRecord{{Played: faultParsedHand(t, card.Rank2)}},
		MustPlay:    true,
		CanBeat:     true,
		DouZeroPos:  DouZeroPosLandlord,
	}
	followContext := GameContext{
		Hand:        faultCards(card.Rank3, card.RankK),
		RecentPlays: [2]PlayRecord{{Played: faultParsedHand(t, card.RankJ)}},
		MustPlay:    false,
		CanBeat:     true,
		DouZeroPos:  DouZeroPosLandlord,
	}

	t.Run("normal response", func(t *testing.T) {
		service := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			assert.Equal(t, "/decide_play", request.URL.Path)
			assert.Equal(t, http.MethodPost, request.Method)
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"action":[4]}`))
		}))
		defer service.Close()

		metrics := observability.NewMetrics(true)
		engine := NewDouZeroEngine(service.URL)
		engine.SetMetrics(metrics)
		got := engine.DecidePlay(context.Background(), "normal", leadContext)

		require.Len(t, got, 1)
		assert.Equal(t, card.Rank4, got[0].Rank)
		assertInvalidActionMetrics(t, metrics, nil)
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

		metrics := observability.NewMetrics(true)
		engine := NewDouZeroEngine(service.URL)
		engine.SetMetrics(metrics)
		got := engine.DecidePlay(ctx, "timeout", leadContext)
		close(release)

		assertLegalFallback(t, leadContext, got)
		assertInvalidActionMetrics(t, metrics, map[invalidActionReason]float64{
			invalidActionTimeout: 1,
		})
	})

	t.Run("connection refusal falls back", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		endpoint := fmt.Sprintf("http://%s", listener.Addr())
		require.NoError(t, listener.Close())

		metrics := observability.NewMetrics(true)
		engine := NewDouZeroEngine(endpoint)
		engine.SetMetrics(metrics)
		got := engine.DecidePlay(context.Background(), "refused", leadContext)

		assertLegalFallback(t, leadContext, got)
		assertInvalidActionMetrics(t, metrics, map[invalidActionReason]float64{
			invalidActionHTTPError: 1,
		})
	})

	for _, testCase := range []struct {
		name         string
		status       int
		body         string
		gameContext  GameContext
		reason       invalidActionReason
		forbiddenLog string
	}{
		{
			name:        "non-2xx with valid action body",
			status:      http.StatusServiceUnavailable,
			body:        `{"action":[4]}`,
			gameContext: leadContext,
			reason:      invalidActionHTTPError,
		},
		{
			name:        "malformed JSON",
			status:      http.StatusOK,
			body:        `{"action":`,
			gameContext: leadContext,
			reason:      invalidActionDecodeError,
		},
		{
			name:        "trailing JSON document",
			status:      http.StatusOK,
			body:        `{"action":[4]}{"action":[5]}`,
			gameContext: leadContext,
			reason:      invalidActionDecodeError,
		},
		{
			name:         "service-declared error",
			status:       http.StatusOK,
			body:         `{"error":"model-error-sensitive-sentinel"}`,
			gameContext:  leadContext,
			reason:       invalidActionHTTPError,
			forbiddenLog: "model-error-sensitive-sentinel",
		},
		{
			name:        "must-play pass",
			status:      http.StatusOK,
			body:        `{"action":[]}`,
			gameContext: leadContext,
			reason:      invalidActionMustPlayPass,
		},
		{
			name:        "card absent from hand",
			status:      http.StatusOK,
			body:        `{"action":[30]}`,
			gameContext: leadContext,
			reason:      invalidActionNotOwned,
		},
		{
			name:        "owned cards form invalid hand",
			status:      http.StatusOK,
			body:        `{"action":[3,4]}`,
			gameContext: leadContext,
			reason:      invalidActionInvalidHand,
		},
		{
			name:        "legal hand cannot beat previous play",
			status:      http.StatusOK,
			body:        `{"action":[3]}`,
			gameContext: followContext,
			reason:      invalidActionCannotBeat,
		},
	} {
		t.Run(testCase.name+" falls back", func(t *testing.T) {
			service := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				response.WriteHeader(testCase.status)
				_, _ = response.Write([]byte(testCase.body))
			}))
			defer service.Close()

			metrics := observability.NewMetrics(true)
			engine := NewDouZeroEngine(service.URL)
			engine.SetMetrics(metrics)
			var capturedLogs bytes.Buffer
			if testCase.forbiddenLog != "" {
				previousWriter := log.Writer()
				log.SetOutput(&capturedLogs)
				t.Cleanup(func() { log.SetOutput(previousWriter) })
			}

			got := engine.DecidePlay(context.Background(), "invalid", testCase.gameContext)

			assertLegalFallback(t, testCase.gameContext, got)
			assertInvalidActionMetrics(t, metrics, map[invalidActionReason]float64{
				testCase.reason: 1,
			})
			if testCase.forbiddenLog != "" {
				assert.NotContains(t, capturedLogs.String(), testCase.forbiddenLog)
			}
		})
	}
}

func TestDouZeroRejectsRedirectWithoutForwardingRequest(t *testing.T) {
	var originRequests atomic.Int32
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		redirectedRequests.Add(1)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"action":[4]}`))
	}))
	defer destination.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		originRequests.Add(1)
		http.Redirect(response, request, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	gameContext := GameContext{
		Hand:       faultCards(card.Rank3, card.Rank4),
		MustPlay:   true,
		CanBeat:    true,
		DouZeroPos: DouZeroPosLandlord,
	}
	metrics := observability.NewMetrics(true)
	engine := NewDouZeroEngine(origin.URL)
	engine.SetMetrics(metrics)

	got := engine.DecidePlay(context.Background(), "redirect", gameContext)

	assertLegalFallback(t, gameContext, got)
	assert.EqualValues(t, 1, originRequests.Load())
	assert.Zero(t, redirectedRequests.Load())
	assertInvalidActionMetrics(t, metrics, map[invalidActionReason]float64{
		invalidActionHTTPError: 1,
	})
}

func TestDouZeroUsesServiceAgainAfterInvalidAction(t *testing.T) {
	gameContext := GameContext{
		Hand:       faultCards(card.Rank3, card.Rank4, card.Rank5),
		MustPlay:   true,
		CanBeat:    true,
		DouZeroPos: DouZeroPosLandlord,
	}
	var requestCount atomic.Int32
	service := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if requestCount.Add(1) == 1 {
			_, _ = response.Write([]byte(`{"action":[30]}`))
			return
		}
		_, _ = response.Write([]byte(`{"action":[4]}`))
	}))
	defer service.Close()

	metrics := observability.NewMetrics(true)
	engine := NewDouZeroEngine(service.URL)
	engine.SetMetrics(metrics)

	first := engine.DecidePlay(context.Background(), "recovering", gameContext)
	assertLegalFallback(t, gameContext, first)
	second := engine.DecidePlay(context.Background(), "recovered", gameContext)
	require.Len(t, second, 1)
	assert.Equal(t, card.Rank4, second[0].Rank)
	assert.EqualValues(t, 2, requestCount.Load())
	assertInvalidActionMetrics(t, metrics, map[invalidActionReason]float64{
		invalidActionNotOwned: 1,
	})
}

func faultCards(ranks ...card.Rank) []card.Card {
	result := make([]card.Card, len(ranks))
	for index, rank := range ranks {
		result[index] = card.Card{Rank: rank, Suit: card.Spade}
	}
	return result
}

func faultParsedHand(t *testing.T, ranks ...card.Rank) rule.ParsedHand {
	t.Helper()
	parsed, err := rule.ParseHand(faultCards(ranks...))
	require.NoError(t, err)
	return parsed
}

func assertLegalFallback(t *testing.T, gameContext GameContext, actual []card.Card) {
	t.Helper()
	expected := ruleFallback(gameContext)
	require.Equal(t, expected, actual)
	require.True(t, faultCardsOwned(gameContext.Hand, actual))
	parsed, err := rule.ParseHand(actual)
	require.NoError(t, err)
	assert.NotEqual(t, rule.Invalid, parsed.Type)
	if !gameContext.MustPlay && !gameContext.RecentPlays[0].Played.IsEmpty() {
		assert.True(t, rule.CanBeat(parsed, gameContext.RecentPlays[0].Played))
	}
}

func faultCardsOwned(hand, played []card.Card) bool {
	available := make(map[card.Card]int, len(hand))
	for _, heldCard := range hand {
		available[heldCard]++
	}
	for _, playedCard := range played {
		if available[playedCard] == 0 {
			return false
		}
		available[playedCard]--
	}
	return true
}

func assertInvalidActionMetrics(t *testing.T, metrics *observability.Metrics, want map[invalidActionReason]float64) {
	t.Helper()
	values := invalidActionMetricValues(t, metrics)
	allowed := []invalidActionReason{
		invalidActionTimeout,
		invalidActionHTTPError,
		invalidActionDecodeError,
		invalidActionNotOwned,
		invalidActionInvalidHand,
		invalidActionCannotBeat,
		invalidActionMustPlayPass,
		invalidActionStaleTurn,
		invalidActionSubmitRejected,
	}
	require.Len(t, values, len(allowed))
	for _, reason := range allowed {
		require.Contains(t, values, string(reason))
		assert.Equal(t, want[reason], values[string(reason)], "reason %s", reason)
	}
}

func invalidActionMetricValues(t *testing.T, metrics *observability.Metrics) map[string]float64 {
	t.Helper()
	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "fight_landlord_bot_invalid_action_total" {
			continue
		}
		values := make(map[string]float64, len(family.GetMetric()))
		for _, metric := range family.GetMetric() {
			require.NotNil(t, metric.Counter)
			for _, label := range metric.GetLabel() {
				if label.GetName() == "reason" {
					values[label.GetValue()] = metric.GetCounter().GetValue()
				}
			}
		}
		return values
	}
	t.Fatal("fight_landlord_bot_invalid_action_total was not gathered")
	return nil
}
