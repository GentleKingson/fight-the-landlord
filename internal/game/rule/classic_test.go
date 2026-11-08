package rule

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/palemoky/fight-the-landlord/internal/game/card"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassicRuleContract(t *testing.T) {
	var fixtures struct {
		Hands []struct {
			ID      string
			Cards   []card.Rank
			Type    string
			KeyRank card.Rank
			Length  int
		}
		Comparisons []struct {
			Play, Target string
			CanBeat      bool
		}
		Replies []struct {
			ID, Target, Type string
			Hand             []card.Rank
			CanBeat          bool
		}
	}
	data, err := os.ReadFile("../../../testdata/classic-rules.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &fixtures))
	hands := make(map[string]ParsedHand)
	for _, tc := range fixtures.Hands {
		t.Run("parse/"+tc.ID, func(t *testing.T) {
			cards := testRuleCards(tc.Cards...)
			parsed, err := ParseHand(cards)
			if tc.Type == "无效" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.Type, parsed.Type.String())
			assert.Equal(t, tc.KeyRank, parsed.KeyRank)
			assert.Equal(t, tc.Length, parsed.Length)
			hands[tc.ID] = parsed
			slices.Reverse(cards)
			reversed, err := ParseHand(cards)
			require.NoError(t, err)
			assert.Equal(t, parsed.Type, reversed.Type)
			assert.Equal(t, parsed.KeyRank, reversed.KeyRank)
		})
	}
	for _, tc := range fixtures.Comparisons {
		t.Run("compare/"+tc.Play+"/"+tc.Target, func(t *testing.T) {
			assert.Equal(t, tc.CanBeat, CanBeat(hands[tc.Play], hands[tc.Target]))
		})
	}
	for _, tc := range fixtures.Replies {
		t.Run("reply/"+tc.ID, func(t *testing.T) {
			hand, target := testRuleCards(tc.Hand...), hands[tc.Target]
			assert.Equal(t, tc.CanBeat, CanBeatWithHand(hand, target))
			before := slices.Clone(hand)
			action := FindSmallestBeatingCards(hand, target)
			assert.Equal(t, before, hand, "提示不能修改手牌")
			if !tc.CanBeat {
				assert.Empty(t, action)
				return
			}
			require.NotEmpty(t, action)
			parsed, err := ParseHand(action)
			require.NoError(t, err)
			assert.True(t, CanBeat(parsed, target))
			assert.Equal(t, tc.Type, parsed.Type.String(), "同型优先于炸弹")
			for _, played := range action {
				index := slices.Index(hand, played)
				require.NotEqual(t, -1, index, "每张出牌都来自手牌且不重复使用")
				hand = slices.Delete(hand, index, index+1)
			}
		})
	}
}
