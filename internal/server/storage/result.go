package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RecordGameResultOnce commits statistics, ranks and the deduplication marker
// together. Retrying after a lost Redis response cannot count the same game twice.
func (lm *LeaderboardManager) RecordGameResultOnce(ctx context.Context, gameID, playerID, playerName string, isLandlord, isWinner bool) error {
	key := playerStatsKey + playerID
	marker := "result:recorded:" + gameID + ":" + playerID
	for attempt := 0; attempt < 10; attempt++ {
		err := lm.redis.Watch(ctx, func(tx *redis.Tx) error {
			recorded, err := tx.Exists(ctx, marker).Result()
			if err != nil || recorded != 0 {
				return err
			}
			data, err := tx.Get(ctx, key).Bytes()
			if err != nil && !errors.Is(err, redis.Nil) {
				return err
			}
			now := time.Now()
			stats := &PlayerStats{PlayerID: playerID, CreatedAt: now.Unix()}
			if len(data) > 0 {
				if err := json.Unmarshal(data, stats); err != nil {
					return err
				}
			}
			stats.PlayerName = playerName
			stats.TotalGames++
			stats.LastPlayedAt = now.Unix()
			scoreChange := updateRoleStats(stats, isLandlord, isWinner)
			updateWinLossStats(stats, isWinner)
			stats.Score = max(0, stats.Score+scoreChange+calculateStreakBonus(stats.CurrentStreak))
			data, err = json.Marshal(stats)
			if err != nil {
				return err
			}
			dailyKey := dailyLeaderboard + now.Format("2006-01-02")
			year, week := now.ISOWeek()
			weeklyKey := fmt.Sprintf("%s%d-W%02d", weeklyLeaderboard, year, week)
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, data, 0)
				rank := redis.Z{Score: float64(stats.Score), Member: playerID}
				pipe.ZAdd(ctx, leaderboardKey, rank)
				pipe.ZAdd(ctx, dailyKey, rank)
				pipe.Expire(ctx, dailyKey, 48*time.Hour)
				pipe.ZAdd(ctx, weeklyKey, rank)
				pipe.Expire(ctx, weeklyKey, 8*24*time.Hour)
				pipe.Set(ctx, marker, "1", 0)
				return nil
			})
			return err
		}, key, marker)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return redis.TxFailedErr
}
