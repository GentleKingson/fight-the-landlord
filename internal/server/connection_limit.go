package server

import (
	"sync"
	"sync/atomic"
)

// connectionLimiter reserves capacity during a handshake and counts only
// upgraded physical WebSocket connections as active. A zero limit is the
// explicit unlimited mode used for maxConnections <= 0. Atomic reservations
// keep memory usage constant even when an operator configures a large limit.
type connectionLimiter struct {
	limit    int64
	reserved atomic.Int64
	active   atomic.Int64
}

func newConnectionLimiter(maxConnections int) *connectionLimiter {
	limiter := &connectionLimiter{}
	if maxConnections > 0 {
		limiter.limit = int64(maxConnections)
	}
	return limiter
}

func (s *Server) activeConnectionLimiter() *connectionLimiter {
	s.connectionLimiterOnce.Do(func() {
		if s.connectionLimiter == nil {
			s.connectionLimiter = newConnectionLimiter(s.maxConnections)
		}
	})
	return s.connectionLimiter
}

func (l *connectionLimiter) tryAcquire() (*connectionLease, bool) {
	if l == nil {
		return &connectionLease{}, true
	}
	if l.limit <= 0 {
		return &connectionLease{limiter: l}, true
	}
	for {
		reserved := l.reserved.Load()
		if reserved >= l.limit {
			return nil, false
		}
		if l.reserved.CompareAndSwap(reserved, reserved+1) {
			return &connectionLease{limiter: l, reserved: true}, true
		}
	}
}

func (l *connectionLimiter) activeCount() int {
	if l == nil {
		return 0
	}
	return int(l.active.Load())
}

// connectionLease belongs to one physical connection, not to a player ID.
// Identity rebinding therefore cannot duplicate or lose capacity. Release is
// idempotent and may be called by competing shutdown paths.
type connectionLease struct {
	limiter *connectionLimiter

	mu        sync.Mutex
	reserved  bool
	activated bool
	released  bool
}

func (l *connectionLease) activate() {
	if l == nil || l.limiter == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released || l.activated {
		return
	}
	l.activated = true
	l.limiter.active.Add(1)
}

func (l *connectionLease) release() {
	if l == nil || l.limiter == nil {
		return
	}
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return
	}
	l.released = true
	reserved := l.reserved
	activated := l.activated
	l.mu.Unlock()

	if activated {
		l.limiter.active.Add(-1)
	}
	if reserved {
		l.limiter.reserved.Add(-1)
	}
}
