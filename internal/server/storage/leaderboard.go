package storage

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	playerStatsKey       = "player:stats:"
	leaderboardKey       = "leaderboard:score"
	dailyLeaderboard     = "leaderboard:daily:"
	weeklyLeaderboard    = "leaderboard:weekly:"
	settledGameKey       = "leaderboard:settlement:"
	dailyLeaderboardTTL  = 48 * time.Hour
	weeklyLeaderboardTTL = 8 * 24 * time.Hour

	LeaderboardTypeTotal    = "total"
	LeaderboardTypeDaily    = "daily"
	LeaderboardTypeWeekly   = "weekly"
	DefaultLeaderboardLimit = 10
	MaxLeaderboardLimit     = 50
)

var (
	ErrInvalidLeaderboardType = errors.New("invalid leaderboard type")
	ErrInvalidGameResult      = errors.New("game ID and player ID are required")
	ErrLeaderboardUnavailable = errors.New("leaderboard storage is unavailable")
)

// PlayerStats 玩家统计数据
type PlayerStats struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`

	// 总计
	TotalGames int `json:"total_games"` // 总场次
	Wins       int `json:"wins"`        // 胜场
	Losses     int `json:"losses"`      // 败场

	// 地主/农民分开统计
	LandlordGames int `json:"landlord_games"` // 地主场次
	LandlordWins  int `json:"landlord_wins"`  // 地主胜场
	FarmerGames   int `json:"farmer_games"`   // 农民场次
	FarmerWins    int `json:"farmer_wins"`    // 农民胜场

	// 积分
	Score int `json:"score"` // 当前积分

	// 连胜/连败
	CurrentStreak int `json:"current_streak"` // 正数为连胜，负数为连败
	MaxWinStreak  int `json:"max_win_streak"` // 最大连胜

	// 时间
	LastPlayedAt int64 `json:"last_played_at"` // 最后游戏时间
	CreatedAt    int64 `json:"created_at"`     // 首次游戏时间
}

// 积分规则
const (
	WinAsLandlord  = 30  // 地主获胜
	WinAsFarmer    = 15  // 农民获胜
	LoseAsLandlord = -20 // 地主失败
	LoseAsFarmer   = -10 // 农民失败

	// 连胜加成
	StreakBonus3  = 5  // 3 连胜加成
	StreakBonus5  = 10 // 5 连胜加成
	StreakBonus10 = 20 // 10 连胜加成
)

// LeaderboardEntry 排行榜条目
type LeaderboardEntry struct {
	Rank       int     `json:"rank"`
	PlayerID   string  `json:"player_id"`
	PlayerName string  `json:"player_name"`
	Score      int     `json:"score"`
	Wins       int     `json:"wins"`
	WinRate    float64 `json:"win_rate"`
}

// LeaderboardManager 排行榜管理器
type LeaderboardManager struct {
	redis *redis.Client
	now   func() time.Time
}

// NewLeaderboardManager 创建排行榜管理器
func NewLeaderboardManager(client *redis.Client, options ...LeaderboardOption) *LeaderboardManager {
	lm := &LeaderboardManager{redis: client, now: time.Now}
	for _, option := range options {
		option(lm)
	}
	return lm
}

// LeaderboardOption customizes a leaderboard manager.
type LeaderboardOption func(*LeaderboardManager)

// WithLeaderboardClock injects the clock used to select daily and weekly buckets.
func WithLeaderboardClock(now func() time.Time) LeaderboardOption {
	return func(lm *LeaderboardManager) {
		if now != nil {
			lm.now = now
		}
	}
}

// IsReady 检查 Redis 客户端是否可用
func (lm *LeaderboardManager) IsReady() bool {
	return lm != nil && lm.redis != nil
}

// GetPlayerStats 获取玩家统计
func (lm *LeaderboardManager) GetPlayerStats(ctx context.Context, playerID string) (*PlayerStats, error) {
	if !lm.IsReady() {
		return nil, ErrLeaderboardUnavailable
	}
	key := playerStatsKey + playerID
	data, err := lm.redis.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}

	var stats PlayerStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// SavePlayerStats 保存玩家统计
func (lm *LeaderboardManager) SavePlayerStats(ctx context.Context, stats *PlayerStats) error {
	if !lm.IsReady() {
		return ErrLeaderboardUnavailable
	}
	key := playerStatsKey + stats.PlayerID
	data, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	return lm.redis.Set(ctx, key, data, 0).Err()
}

// updateRoleStats 更新角色相关统计并返回基础积分变化
func updateRoleStats(stats *PlayerStats, isLandlord, isWinner bool) int {
	switch {
	case isLandlord && isWinner:
		stats.LandlordGames++
		stats.LandlordWins++
		return WinAsLandlord
	case isLandlord && !isWinner:
		stats.LandlordGames++
		return LoseAsLandlord
	case !isLandlord && isWinner:
		stats.FarmerGames++
		stats.FarmerWins++
		return WinAsFarmer
	default: // !isLandlord && !isWinner
		stats.FarmerGames++
		return LoseAsFarmer
	}
}

// updateWinLossStats 更新胜负统计和连胜/连败
func updateWinLossStats(stats *PlayerStats, isWinner bool) {
	if isWinner {
		stats.Wins++
		stats.CurrentStreak = max(1, stats.CurrentStreak+1)
	} else {
		stats.Losses++
		stats.CurrentStreak = min(-1, stats.CurrentStreak-1)
	}

	if stats.CurrentStreak > stats.MaxWinStreak {
		stats.MaxWinStreak = stats.CurrentStreak
	}
}

// calculateStreakBonus 计算连胜加成
func calculateStreakBonus(streak int) int {
	switch {
	case streak >= 10:
		return StreakBonus10
	case streak >= 5:
		return StreakBonus5
	case streak >= 3:
		return StreakBonus3
	default:
		return 0
	}
}

// RecordGameResult records one player's settlement exactly once per game.
// WATCH keeps the JSON read-modify-write and all leaderboard indexes atomic.
func (lm *LeaderboardManager) RecordGameResult(
	ctx context.Context,
	gameID, playerID, playerName string,
	isLandlord, isWinner bool,
) error {
	if !lm.IsReady() {
		return ErrLeaderboardUnavailable
	}
	if strings.TrimSpace(gameID) == "" || strings.TrimSpace(playerID) == "" {
		return ErrInvalidGameResult
	}

	now := lm.now()
	statsKey := playerStatsKey + playerID
	settlementKey := settledGameKey + gameID
	dailyKey := dailyLeaderboardKey(now)
	weeklyKey := weeklyLeaderboardKey(now)

	for range 32 {
		err := lm.redis.Watch(ctx, func(tx *redis.Tx) error {
			settled, err := tx.SIsMember(ctx, settlementKey, playerID).Result()
			if err != nil {
				return err
			}
			if settled {
				return nil
			}

			stats, err := getOrCreateStatsFromCmd(ctx, tx, statsKey, playerID, playerName, now)
			if err != nil {
				return err
			}
			stats.PlayerName = playerName
			stats.TotalGames++
			stats.LastPlayedAt = now.Unix()

			previousScore := stats.Score
			scoreChange := updateRoleStats(stats, isLandlord, isWinner)
			updateWinLossStats(stats, isWinner)
			scoreChange += calculateStreakBonus(stats.CurrentStreak)
			stats.Score = max(0, stats.Score+scoreChange)
			periodScoreChange := stats.Score - previousScore

			data, err := json.Marshal(stats)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, statsKey, data, 0)
				pipe.ZAdd(ctx, leaderboardKey, redis.Z{Score: float64(stats.Score), Member: playerID})
				pipe.ZIncrBy(ctx, dailyKey, float64(periodScoreChange), playerID)
				pipe.Expire(ctx, dailyKey, dailyLeaderboardTTL)
				pipe.ZIncrBy(ctx, weeklyKey, float64(periodScoreChange), playerID)
				pipe.Expire(ctx, weeklyKey, weeklyLeaderboardTTL)
				pipe.SAdd(ctx, settlementKey, playerID)
				return nil
			})
			return err
		}, statsKey, settlementKey)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return redis.TxFailedErr
}

type statsGetter interface {
	Get(context.Context, string) *redis.StringCmd
}

func getOrCreateStatsFromCmd(
	ctx context.Context,
	cmd statsGetter,
	key, playerID, playerName string,
	now time.Time,
) (*PlayerStats, error) {
	data, err := cmd.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return &PlayerStats{
			PlayerID: playerID, PlayerName: playerName, CreatedAt: now.Unix(),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	var stats PlayerStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// NormalizeLeaderboardType applies the backwards-compatible default and rejects unknown boards.
func NormalizeLeaderboardType(leaderboardType string) (string, error) {
	leaderboardType = strings.TrimSpace(strings.ToLower(leaderboardType))
	if leaderboardType == "" {
		return LeaderboardTypeTotal, nil
	}
	switch leaderboardType {
	case LeaderboardTypeTotal, LeaderboardTypeDaily, LeaderboardTypeWeekly:
		return leaderboardType, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidLeaderboardType, leaderboardType)
	}
}

// NormalizeLeaderboardPagination bounds public queries to a small, predictable page.
func NormalizeLeaderboardPagination(offset, limit int) (int, int) {
	offset = max(offset, 0)
	if limit <= 0 {
		limit = DefaultLeaderboardLimit
	}
	limit = min(limit, MaxLeaderboardLimit)
	return offset, limit
}

// GetLeaderboard returns one validated leaderboard page.
func (lm *LeaderboardManager) GetLeaderboard(
	ctx context.Context,
	leaderboardType string,
	offset, limit int,
) ([]*LeaderboardEntry, error) {
	if !lm.IsReady() {
		return nil, ErrLeaderboardUnavailable
	}
	leaderboardType, err := NormalizeLeaderboardType(leaderboardType)
	if err != nil {
		return nil, err
	}
	offset, limit = NormalizeLeaderboardPagination(offset, limit)
	key := lm.leaderboardKey(leaderboardType, lm.now())

	// 获取排行榜（从高到低）
	results, err := lm.redis.ZRevRangeWithScores(ctx, key, int64(offset), int64(offset+limit-1)).Result()
	if err != nil {
		return nil, err
	}

	entries := make([]*LeaderboardEntry, 0, len(results))
	for i, result := range results {
		playerID, ok := result.Member.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected leaderboard member type %T", result.Member)
		}

		// 获取玩家详细统计
		stats, err := lm.GetPlayerStats(ctx, playerID)
		if err != nil {
			return nil, err
		}
		if stats == nil {
			continue
		}

		winRate := 0.0
		if stats.TotalGames > 0 {
			winRate = float64(stats.Wins) / float64(stats.TotalGames) * 100
		}

		entries = append(entries, &LeaderboardEntry{
			Rank:       offset + i + 1,
			PlayerID:   playerID,
			PlayerName: stats.PlayerName,
			Score:      int(result.Score),
			Wins:       stats.Wins,
			WinRate:    winRate,
		})
	}

	return entries, nil
}

func (lm *LeaderboardManager) leaderboardKey(leaderboardType string, now time.Time) string {
	switch leaderboardType {
	case LeaderboardTypeDaily:
		return dailyLeaderboardKey(now)
	case LeaderboardTypeWeekly:
		return weeklyLeaderboardKey(now)
	default:
		return leaderboardKey
	}
}

func dailyLeaderboardKey(now time.Time) string {
	return dailyLeaderboard + now.Format("2006-01-02")
}

func weeklyLeaderboardKey(now time.Time) string {
	year, week := now.ISOWeek()
	return fmt.Sprintf("%s%d-W%02d", weeklyLeaderboard, year, week)
}

// GetPlayerRank 获取玩家排名
func (lm *LeaderboardManager) GetPlayerRank(ctx context.Context, playerID string) (int64, error) {
	if !lm.IsReady() {
		return -1, ErrLeaderboardUnavailable
	}
	rank, err := lm.redis.ZRevRank(ctx, leaderboardKey, playerID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return -1, nil // 未上榜
		}
		return -1, err
	}
	return rank + 1, nil // Redis 排名从 0 开始
}

// SortByScore 按积分排序
func SortByScore(entries []LeaderboardEntry) {
	slices.SortFunc(entries, func(a, b LeaderboardEntry) int {
		return cmp.Compare(b.Score, a.Score)
	})
}
