package observability

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisHook records command latency without exposing command arguments, keys,
// addresses, or credentials. Operation names are allowlisted by Metrics.
type RedisHook struct {
	metrics *Metrics
}

func NewRedisHook(metrics *Metrics) *RedisHook { return &RedisHook{metrics: metrics} }

func (h *RedisHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		started := time.Now()
		conn, err := next(ctx, network, addr)
		h.observe("dial", started, err)
		return conn, err
	}
}

func (h *RedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		started := time.Now()
		err := next(ctx, cmd)
		operation := "other"
		if cmd != nil {
			operation = cmd.Name()
		}
		h.observe(operation, started, err)
		return err
	}
}

func (h *RedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		started := time.Now()
		err := next(ctx, cmds)
		h.observe("pipeline", started, err)
		return err
	}
}

func (h *RedisHook) observe(operation string, started time.Time, err error) {
	if h == nil || h.metrics == nil {
		return
	}
	failed := err != nil && !errors.Is(err, redis.Nil)
	h.metrics.ObserveRedis(operation, time.Since(started), failed)
}
