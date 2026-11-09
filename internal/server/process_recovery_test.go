package server

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/bot"
	"github.com/palemoky/fight-the-landlord/internal/config"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/convert"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// This subprocess is intentionally killed without cleanup to test crash recovery.
func TestRecoveryServerProcess(t *testing.T) {
	if os.Getenv("DDZ_RECOVERY_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	cfg := config.Default()
	cfg.Redis.Addr = os.Getenv("DDZ_RECOVERY_REDIS")
	cfg.Game.BidTimeout, cfg.Game.TurnTimeout = 600, 600
	cfg.Security.RateLimit.MaxPerSecond, cfg.Security.RateLimit.MaxPerMinute = 1000, 10000
	s, err := NewServer(cfg)
	require.NoError(t, err)
	restored := s.handler.GameSessions()
	require.NoError(t, s.restoreGames(context.Background(), bot.NewHeuristicEngine()))
	require.ElementsMatch(t, restored, s.handler.GameSessions(), "repeat startup restoration must not replace running sessions")
	web := httptest.NewServer(s.routes())
	root, err := os.OpenRoot(os.Getenv("DDZ_RECOVERY_DIR"))
	require.NoError(t, err)
	defer root.Close()
	require.NoError(t, root.WriteFile("url", []byte(web.URL), 0o600))
	select {}
}

func startRecoveryProcess(t *testing.T, redisAddr string) (url string, kill func()) {
	t.Helper()
	dir := t.TempDir()
	urlFile := filepath.Join(dir, "url")
	bin, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(bin, "-test.run=^TestRecoveryServerProcess$")
	cmd.Env = append(os.Environ(), "DDZ_RECOVERY_CHILD=1", "DDZ_RECOVERY_REDIS="+redisAddr, "DDZ_RECOVERY_DIR="+dir)
	logFile, err := os.Create(filepath.Join(dir, "server.log"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = logFile.Close() })
	cmd.Stdout, cmd.Stderr = logFile, logFile
	require.NoError(t, cmd.Start())
	var once sync.Once
	kill = func() { once.Do(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) }
	t.Cleanup(kill)
	require.Eventually(t, func() bool { data, err := os.ReadFile(urlFile); url = string(data); return err == nil && url != "" }, 10*time.Second, 10*time.Millisecond)
	return url, kill
}

func startRealRedis(t *testing.T, dir, port, aof string) (addr string, kill func()) {
	t.Helper()
	cmd := exec.Command("redis-server", "--bind", "127.0.0.1", "--port", port, "--dir", dir,
		"--appendonly", aof, "--appendfsync", "always", "--save", "", "--logfile", filepath.Join(dir, "redis.log"))
	require.NoError(t, cmd.Start())
	var once sync.Once
	kill = func() { once.Do(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) }
	t.Cleanup(kill)
	addr = "127.0.0.1:" + port
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	require.Eventually(t, func() bool { return rdb.Ping(context.Background()).Err() == nil }, 5*time.Second, 20*time.Millisecond)
	return addr, kill
}

func unusedRedisPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, listener.Close())
	return port
}

func TestProcessCrashRedisRestartAndBackupRecovery(t *testing.T) {
	if os.Getenv("DDZ_REDIS_INTEGRATION") != "1" {
		t.Skip("set DDZ_REDIS_INTEGRATION=1 with redis-server and redis-cli on PATH")
	}
	redisDir, port := t.TempDir(), unusedRedisPort(t)
	addr, killRedis := startRealRedis(t, redisDir, port, "yes")
	url, killServer := startRecoveryProcess(t, addr)
	first, cookies := dialIdentity(t, url, nil, true)
	identity := receivePayload[protocol.ReconnectedPayload](t, first, protocol.MsgReconnected)
	for range 2 {
		other, _ := dialIdentity(t, url, nil, true)
		receivePayload[protocol.ReconnectedPayload](t, other, protocol.MsgReconnected)
		sendIdentityMessage(t, other, protocol.MsgQuickMatch, nil)
	}
	sendIdentityMessage(t, first, protocol.MsgQuickMatch, nil)
	receivePayload[protocol.DealCardsPayload](t, first, protocol.MsgDealCards)
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	store := storage.NewRedisStore(rdb)
	records, err := store.LoadGames(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	record := records[0]
	snapshot, err := session.DecodeSnapshot(record)
	require.NoError(t, err)
	var hand []protocol.CardInfo
	for _, p := range snapshot.Players {
		if p.ID == identity.PlayerID {
			hand = convert.CardsToInfos(p.Hand)
		}
	}
	checkRecovered := func(endpoint string) {
		conn, _ := dialIdentity(t, endpoint, cookies[0], true)
		got := receivePayload[protocol.ReconnectedPayload](t, conn, protocol.MsgReconnected)
		require.Equal(t, identity.PlayerID, got.PlayerID)
		require.Equal(t, record.RoomCode, got.RoomCode)
		require.NotNil(t, got.GameState)
		require.Equal(t, record.ID, got.GameState.GameID)
		require.Equal(t, hand, got.GameState.Hand)
		require.GreaterOrEqual(t, got.GameState.StateVersion, record.Revision)
	}
	killServer() // SIGKILL: no graceful shutdown and no disconnect checkpoint.
	url, killServer = startRecoveryProcess(t, addr)
	checkRecovered(url)
	killServer()
	killRedis() // AOF must retain even the last acknowledged snapshot.
	addr, _ = startRealRedis(t, redisDir, port, "yes")
	url, killServer = startRecoveryProcess(t, addr)
	checkRecovered(url)
	killServer()
	backup := filepath.Join(t.TempDir(), "backup.rdb")
	output, err := exec.Command("bash", "../../scripts/backup-redis.sh", backup, "-h", "127.0.0.1", "-p", port).CombinedOutput()
	require.NoError(t, err, "%s", output)
	data, err := os.ReadFile(backup)
	require.NoError(t, err)
	restoredDir, restoredPort := t.TempDir(), unusedRedisPort(t)
	root, err := os.OpenRoot(restoredDir)
	require.NoError(t, err)
	require.NoError(t, root.WriteFile("dump.rdb", data, 0o600))
	require.NoError(t, root.Close())
	restoredAddr, stopRestored := startRealRedis(t, restoredDir, restoredPort, "no")
	url, stopRecovered := startRecoveryProcess(t, restoredAddr)
	checkRecovered(url)
	stopRecovered()
	// Convert the offline RDB restore into a fresh AOF before putting it in service.
	restoredRedis := redis.NewClient(&redis.Options{Addr: restoredAddr})
	defer restoredRedis.Close()
	require.NoError(t, restoredRedis.ConfigSet(context.Background(), "appendonly", "yes").Err())
	require.Eventually(t, func() bool {
		info, err := restoredRedis.Info(context.Background(), "persistence").Result()
		return err == nil && strings.Contains(info, "aof_rewrite_in_progress:0") && strings.Contains(info, "aof_last_bgrewrite_status:ok")
	}, 5*time.Second, 20*time.Millisecond)
	stopRestored()
	restoredAddr, _ = startRealRedis(t, restoredDir, restoredPort, "yes")
	url, _ = startRecoveryProcess(t, restoredAddr)
	checkRecovered(url)
}
