package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/palemoky/fight-the-landlord/internal/game/rule"
	"github.com/stretchr/testify/require"
)

func TestDouZeroUsesServerRulesBeforeReturningAction(t *testing.T) {
	for _, tc := range []struct {
		name, hand, target string
		action             []int
		want               rule.HandType
	}{
		{"pair_as_single_wings", "4 4 4 5 5 5 6 6", "3 3 3 4 4 4 A 2", []int{4, 4, 4, 5, 5, 5, 6, 6}, rule.PlaneWithSingles},
		{"reject_two_quads", "4 4 4 4 5 5 5 5", "3 3 3 3 6 6 7 7", []int{4, 4, 4, 4, 5, 5, 5, 5}, rule.Bomb},
		{"reject_weaker_reply", "3 4 4 4 5 5 5 6 6", "3 3 3 4 4 4 A 2", []int{3}, rule.PlaneWithSingles},
		{"reject_unowned_cards", "4 4 4 5 5 5 6 6", "3 3 3 4 4 4 A 2", []int{30}, rule.PlaneWithSingles},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(douzeroResponse{Action: tc.action})
			}))
			defer server.Close()
			gctx := GameContext{Hand: cards(tc.hand), RecentPlays: [2]PlayRecord{play(tc.target, true), {}}, CanBeat: true, DouZeroPos: DouZeroPosLandlordDn}
			action := NewDouZeroEngine(server.URL).DecidePlay(context.Background(), "test", gctx)
			require.NotEmpty(t, action)
			parsed, err := rule.ParseHand(action)
			require.NoError(t, err)
			require.True(t, rule.CanBeat(parsed, gctx.RecentPlays[0].Played))
			require.Equal(t, tc.want, parsed.Type)
		})
	}
}
