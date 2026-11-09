package storage

import (
	"context"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestIdentityVerificationExpiryAndFailure(t *testing.T) {
	store, redis := newTestRedisStore(t)
	defer redis.Close()
	ctx := context.Background()
	identity, token, err := store.CreateIdentity(ctx, "持久昵称", time.Hour)
	require.NoError(t, err)
	require.NotContains(t, redis.Dump(), token)
	found, err := store.VerifyIdentity(ctx, identity.ID, token)
	require.NoError(t, err)
	require.Equal(t, identity.Name, found.Name)
	_, err = store.VerifyIdentity(ctx, identity.ID, strings.Repeat("0", 64))
	require.ErrorIs(t, err, ErrInvalidIdentity)
	redis.SetError("ERR unavailable")
	_, err = store.VerifyIdentity(ctx, identity.ID, token)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrInvalidIdentity)
	_, _, err = store.CreateIdentity(ctx, "unknown", time.Hour)
	require.Error(t, err)
	redis.SetError("")
	redis.FastForward(time.Hour)
	_, err = store.VerifyIdentity(ctx, identity.ID, token)
	require.ErrorIs(t, err, ErrInvalidIdentity)
}

func TestResultCommitIsIdempotentAcrossRedisFailure(t *testing.T) {
	lb, redis := newTestLeaderboardManager(t)
	defer redis.Close()
	ctx := context.Background()
	redis.SetError("ERR unavailable")
	require.Error(t, lb.RecordGameResultOnce(ctx, "game1", "p1", "P1", true, true))
	redis.SetError("")
	for range 3 {
		require.NoError(t, lb.RecordGameResultOnce(ctx, "game1", "p1", "P1", true, true))
	}
	stats, err := lb.GetPlayerStats(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, 1, stats.TotalGames)
	require.Equal(t, 30, stats.Score)
	require.NoError(t, lb.RecordGameResultOnce(ctx, "game2", "p1", "P1", false, true))
	stats, err = lb.GetPlayerStats(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, 2, stats.TotalGames)
}
