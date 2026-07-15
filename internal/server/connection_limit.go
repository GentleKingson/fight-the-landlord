package server

import (
	"sync"
	"sync/atomic"
)

// connectionLimiter reserves capacity during a handshake and counts only
// upgraded physical WebSocket connections as active. A nil slots channel is
// the explicit unlimited mode used for maxConnections <= 0.
type connectionLimiter struct {
	slots  chan struct{}
	active atomic.Int64
}

func newConnectionLimiter(maxConnections int) *connectionLimiter {
	limiter := &connectionLimiter{}
	if maxConnections > 0 {
		limiter.slots = make(chan struct{}, maxConnections)
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
	if l.slots != nil {
		select {
		case l.slots <- struct{}{}:
		default:
			return nil, false
		}
	}
	return &connectionLease{limiter: l}, true
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
	activated := l.activated
	l.mu.Unlock()

	if activated {
		l.limiter.active.Add(-1)
	}
	if l.limiter.slots != nil {
		<-l.limiter.slots
	}
}
