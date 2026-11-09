package server

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/game/room"
	"github.com/palemoky/fight-the-landlord/internal/server/handler"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSnapshotCleanupWaitsForSessionAndHandlesMissingOfflineCheckpoint(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	store := storage.NewRedisStore(rdb)
	r := room.NewMockRoom("123456", nil)
	for i, id := range []string{"p1", "p2", "p3"} {
		r.Players[id] = &room.RoomPlayer{Client: testutil.NewSimpleClient(id, id), Seat: i}
		r.PlayerOrder = append(r.PlayerOrder, id)
	}
	gs := session.NewGameSession(r, storage.NewLeaderboardManager(rdb), config.GameConfig{OfflineRoomRetention: 1})
	defer gs.Suspend()
	gs.Start()
	gs.Suspend()
	records, err := store.LoadGames(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	record := records[0]
	// Model the last online checkpoint left behind when every disconnect write
	// failed and room cleanup eventually stopped the process-owned retry.
	require.True(t, record.ExpiresAt.IsZero())
	record.SavedAt = time.Now().Add(-time.Hour)
	record.Revision++
	require.NoError(t, store.SaveGame(context.Background(), record))
	h := handler.NewHandler(handler.HandlerDeps{})
	s := &Server{redisStore: store, handler: h}
	h.SetGameSession(r.Code, gs)
	require.NoError(t, s.cleanPersistedGames(context.Background()))
	got, err := store.LoadGame(context.Background(), record.ID)
	require.NoError(t, err)
	require.NotNil(t, got, "the registered session may still need its CAS predecessor")
	h.SetGameSession(r.Code, nil)
	require.NoError(t, s.cleanPersistedGames(context.Background()))
	got, err = store.LoadGame(context.Background(), record.ID)
	require.NoError(t, err)
	require.Nil(t, got, "an abandoned online snapshot must eventually expire too")
}
