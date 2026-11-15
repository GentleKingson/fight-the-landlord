package handler

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestHandleGetLeaderboard_ForwardsTypeOffsetAndNormalizesUnknownType(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	ldb := storage.NewLeaderboardManager(rdb)
	ctx := context.Background()

	// Historical player only in total leaderboard
	require.NoError(t, ldb.SavePlayerStats(ctx, &storage.PlayerStats{
		PlayerID: "legacy", PlayerName: "Legacy", TotalGames: 5, Wins: 5, Score: 200,
	}))
	require.NoError(t, rdb.ZAdd(ctx, "leaderboard:score", redis.Z{Score: 200, Member: "legacy"}).Err())

	// Active today/weekly players
	require.NoError(t, ldb.RecordGameResult(ctx, "p1", "Alice", true, true)) // 30
	require.NoError(t, ldb.RecordGameResult(ctx, "p2", "Bob", false, true))  // 15

	h := NewHandler(HandlerDeps{Leaderboard: ldb})
	client := testutil.NewSimpleClient("p1", "Alice")

	// 1. Daily offset=1 limit=1 returns Rank=2 (Bob) and Type="daily"
	h.Handle(client, codec.MustNewMessage(protocol.MsgGetLeaderboard, protocol.GetLeaderboardPayload{
		Type:   "daily",
		Offset: 1,
		Limit:  1,
	}))
	msgs := client.Messages
	require.Len(t, msgs, 1)
	dailyRes, err := codec.ParsePayload[protocol.LeaderboardResultPayload](msgs[0])
	require.NoError(t, err)
	require.Equal(t, "daily", dailyRes.Type)
	require.Len(t, dailyRes.Entries, 1)
	require.Equal(t, "p2", dailyRes.Entries[0].PlayerID)
	require.Equal(t, 2, dailyRes.Entries[0].Rank)

	// 2. Unknown Type normalizes to "total" in both query and response payload
	h.Handle(client, codec.MustNewMessage(protocol.MsgGetLeaderboard, protocol.GetLeaderboardPayload{
		Type:   "bogus",
		Offset: 0,
		Limit:  2,
	}))
	msgs = client.Messages
	require.Len(t, msgs, 2)
	totalRes, err := codec.ParsePayload[protocol.LeaderboardResultPayload](msgs[1])
	require.NoError(t, err)
	require.Equal(t, "total", totalRes.Type)
	require.Len(t, totalRes.Entries, 2)
	require.Equal(t, "legacy", totalRes.Entries[0].PlayerID)
	require.Equal(t, 1, totalRes.Entries[0].Rank)
	require.Equal(t, "p1", totalRes.Entries[1].PlayerID)
	require.Equal(t, 2, totalRes.Entries[1].Rank)
}
