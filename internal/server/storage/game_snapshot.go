package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const gameKeyPrefix = "game:v2:"
const pendingGamesKey = "game:pending-results"

var ErrStaleGame = errors.New("game snapshot revision conflict")

// GameRecord is never a wire payload: State contains every player's private hand.
// Revision fences delayed writers; ID remains stable across deals and restarts.
type GameRecord struct {
	Version   int             `json:"version"`
	ID        string          `json:"id"`
	RoomCode  string          `json:"room_code"`
	Revision  int64           `json:"revision"`
	SavedAt   time.Time       `json:"saved_at"`
	ExpiresAt time.Time       `json:"expires_at"`
	Ended     bool            `json:"ended"`
	State     json.RawMessage `json:"state"`
	Results   []GameResult    `json:"results,omitempty"`
}

// The snapshot and durable settlement outbox are committed together. Exact
// retries also succeed after an interrupted response; conflicting retries fail.
var saveGameScript = redis.NewScript(`
local current = redis.call('HGET', KEYS[1], 'revision')
if current and tonumber(current) == tonumber(ARGV[2]) then
  if redis.call('HGET', KEYS[1], 'data') == ARGV[3] then return 1 end
  return 0
end
if (tonumber(current) or 0) ~= tonumber(ARGV[1]) then return 0 end
redis.call('HSET', KEYS[1], 'revision', ARGV[2], 'data', ARGV[3])
if ARGV[4] == '1' and redis.call('HEXISTS', KEYS[1], 'settlement') == 0 then
  local seq = redis.call('INCR', KEYS[3])
  redis.call('HSET', KEYS[1], 'settlement', 'pending')
  redis.call('ZADD', KEYS[2], seq, ARGV[5])
end
return 1
`)

func (rs *RedisStore) SaveGame(ctx context.Context, record *GameRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	ended := "0"
	if record.Ended {
		ended = "1"
	}
	ok, err := saveGameScript.Run(ctx, rs.client, []string{gameKeyPrefix + record.ID, pendingGamesKey, "game:finish-sequence"},
		record.Revision-1, record.Revision, data, ended, record.ID).Int()
	if err != nil {
		return err
	}
	if ok != 1 {
		return ErrStaleGame
	}
	return nil
}

func (rs *RedisStore) LoadGame(ctx context.Context, id string) (*GameRecord, error) {
	data, err := rs.client.HGet(ctx, gameKeyPrefix+id, "data").Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record GameRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if record.Version != 2 || record.ID != id || record.Revision < 1 {
		return nil, fmt.Errorf("unsupported or invalid game snapshot %s", id)
	}
	return &record, nil
}

func (rs *RedisStore) LoadGames(ctx context.Context) ([]*GameRecord, error) {
	var records []*GameRecord
	seen := make(map[string]bool)
	iter := rs.client.Scan(ctx, 0, gameKeyPrefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		id := iter.Val()[len(gameKeyPrefix):]
		if seen[id] {
			continue
		}
		seen[id] = true
		record, err := rs.LoadGame(ctx, id)
		if err != nil {
			return nil, err
		}
		if record != nil {
			records = append(records, record)
		}
	}
	return records, iter.Err()
}

// DeleteExpiredGame never discards a pending settlement, even after room expiry.
func (rs *RedisStore) DeleteExpiredGame(ctx context.Context, record *GameRecord) error {
	return rs.client.Eval(ctx, `
if redis.call('HGET', KEYS[1], 'revision') ~= ARGV[1] then return 0 end
if redis.call('HGET', KEYS[1], 'settlement') == 'pending' then return 0 end
return redis.call('DEL', KEYS[1])`, []string{gameKeyPrefix + record.ID}, record.Revision).Err()
}

// DrainSettlements preserves each player's finish order without blocking other
// players behind a failing result. Only one server instance may run this worker.
func (rs *RedisStore) DrainSettlements(ctx context.Context, lb *LeaderboardManager) error {
	ids, err := rs.client.ZRange(ctx, pendingGamesKey, 0, -1).Result()
	if err != nil {
		return err
	}
	blocked := make(map[string]bool)
	for _, id := range ids {
		record, err := rs.LoadGame(ctx, id)
		if err != nil {
			return err
		}
		if record == nil {
			return fmt.Errorf("missing pending settlement %s", id)
		}
		complete := true
		for _, result := range record.Results {
			if blocked[result.PlayerID] {
				complete = false
				continue
			}
			if err := lb.RecordGameResultOnce(ctx, id, result.PlayerID, result.PlayerName, result.IsLandlord, result.IsWinner); err != nil {
				blocked[result.PlayerID], complete = true, false
			}
		}
		if complete {
			_, err = rs.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.HSet(ctx, gameKeyPrefix+id, "settlement", "done")
				pipe.ZRem(ctx, pendingGamesKey, id)
				return nil
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (lm *LeaderboardManager) GameStore() *RedisStore { return NewRedisStore(lm.redis) }
