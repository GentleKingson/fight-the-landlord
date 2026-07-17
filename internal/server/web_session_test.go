package server

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func issueTestWebSessionTicket(
	t *testing.T,
	manager *webSessionTicketManager,
	token, predecessor string,
	rollback, orphan func() bool,
) (ticket, owner string) {
	t.Helper()
	if predecessor == "" {
		var err error
		owner, err = manager.AcquireOwnerNonce()
		require.NoError(t, err)
	}
	ticket, err := manager.Issue(
		token,
		predecessor,
		owner,
		func() bool { return true },
		nil,
		rollback,
		orphan,
	)
	require.NoError(t, err)
	return ticket, owner
}

func TestWebSessionTicketIsOpaqueBoundIdempotentUntilObservedAndExpires(t *testing.T) {
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	manager := newWebSessionTicketManager()
	manager.reader = strings.NewReader(strings.Repeat("a", 64))
	manager.now = func() time.Time { return now }

	ticket, owner := issueTestWebSessionTicket(t, manager, "opaque-reconnect-token", "predecessor", nil, nil)
	assert.Len(t, ticket, 64)
	assert.NotContains(t, ticket, "opaque-reconnect-token")

	token, ok := manager.Commit(ticket, "predecessor", owner, func(string) bool { return true })
	assert.True(t, ok)
	assert.Equal(t, "opaque-reconnect-token", token)
	token, ok = manager.Commit(ticket, "predecessor", owner, func(string) bool { return true })
	assert.True(t, ok, "a response-loss retry must reissue the same successor")
	assert.Equal(t, "opaque-reconnect-token", token)
	assert.Equal(t, 1, manager.ObserveSuccessor(token))
	_, ok = manager.Commit(ticket, "predecessor", owner, func(string) bool { return true })
	assert.False(t, ok, "successor observation retires the ticket")

	ticket, owner = issueTestWebSessionTicket(t, manager, "another-token", "", nil, nil)
	now = now.Add(webSessionTicketTTL)
	_, ok = manager.Commit(ticket, "", owner, func(string) bool { return true })
	assert.False(t, ok, "ticket expires exactly at its deadline")
}

func TestWebSessionTicketCanBeInvalidated(t *testing.T) {
	manager := newWebSessionTicketManager()
	ticket, owner := issueTestWebSessionTicket(t, manager, "opaque-reconnect-token", "", nil, nil)
	manager.Invalidate(ticket)
	_, ok := manager.Commit(ticket, "", owner, func(string) bool { return true })
	assert.False(t, ok)
}

func TestWebSessionTicketBindingMismatchPreservesOwnerRetry(t *testing.T) {
	manager := newWebSessionTicketManager()
	predecessor := strings.Repeat("a", 64)
	successor := strings.Repeat("b", 64)
	rollbacks := 0
	ticket, owner := issueTestWebSessionTicket(t, manager, successor, predecessor, func() bool {
		rollbacks++
		return true
	}, nil)

	_, ok := manager.Commit(ticket, strings.Repeat("c", 64), owner, func(string) bool { return true })
	assert.False(t, ok)
	_, ok = manager.Commit(ticket, "", owner, func(string) bool { return true })
	assert.False(t, ok)
	assert.Zero(t, rollbacks, "a forged request must not invalidate the owner's ticket")

	token, ok := manager.Commit(ticket, predecessor, owner, func(string) bool { return true })
	assert.True(t, ok)
	assert.Equal(t, successor, token)
}

func TestWebSessionTicketValidationFailureAndExpiryRollback(t *testing.T) {
	t.Run("validation failure", func(t *testing.T) {
		manager := newWebSessionTicketManager()
		rollbacks := 0
		ticket, owner := issueTestWebSessionTicket(t, manager, "successor", "predecessor", func() bool {
			rollbacks++
			return true
		}, nil)

		_, ok := manager.Commit(ticket, "predecessor", owner, func(string) bool { return false })
		assert.False(t, ok)
		assert.Equal(t, 1, rollbacks)
		assert.Empty(t, manager.entries)
		assert.Empty(t, manager.successorTickets)
	})

	t.Run("deadline is inclusive", func(t *testing.T) {
		now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
		manager := newWebSessionTicketManager()
		manager.now = func() time.Time { return now }
		rollbacks := 0
		_, _ = issueTestWebSessionTicket(t, manager, "successor", "predecessor", func() bool {
			rollbacks++
			return true
		}, nil)

		now = now.Add(webSessionTicketTTL)
		assert.Equal(t, 1, manager.PurgeExpired())
		assert.Equal(t, 1, rollbacks)
		assert.Empty(t, manager.entries)
		assert.Empty(t, manager.successorTickets)
	})
}

func TestCommittedWebSessionTicketCloseOrphansInsteadOfRollingBack(t *testing.T) {
	manager := newWebSessionTicketManager()
	rollbacks := 0
	orphans := 0
	ticket, owner := issueTestWebSessionTicket(t, manager, "successor", "predecessor", func() bool {
		rollbacks++
		return true
	}, func() bool {
		orphans++
		return true
	})
	_, ok := manager.Commit(ticket, "predecessor", owner, func(string) bool { return true })
	require.True(t, ok)

	assert.True(t, manager.Invalidate(ticket))
	assert.Zero(t, rollbacks)
	assert.Equal(t, 1, orphans)
}

func TestWebSessionTicketSuccessorIndexStaysConsistentConcurrently(t *testing.T) {
	manager := newWebSessionTicketManager()
	const count = 64
	tickets := make([]string, count)
	tokens := make([]string, count)
	for i := range count {
		tokens[i] = fmt.Sprintf("%064x", i+1)
		var owner string
		tickets[i], owner = issueTestWebSessionTicket(t, manager, tokens[i], "", nil, nil)
		_, ok := manager.Commit(tickets[i], "", owner, func(string) bool { return true })
		require.True(t, ok)
	}

	var workers sync.WaitGroup
	for i := range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if i%2 == 0 {
				assert.Equal(t, 1, manager.ObserveSuccessor(tokens[i]))
				return
			}
			assert.True(t, manager.Invalidate(tickets[i]))
		}()
	}
	workers.Wait()
	assert.Empty(t, manager.entries)
	assert.Empty(t, manager.successorTickets)
}

func TestClosingBrowserClientInvalidatesUncommittedProvisionalTicket(t *testing.T) {
	manager := newWebSessionTicketManager()
	server := &Server{webSessionTickets: manager}
	client := NewClient(server, nil)
	client.browserTransport = true
	owner, err := manager.AcquireOwnerNonce()
	require.NoError(t, err)
	client.browserTicketOwnerToken = owner
	ticket, err := client.IssueWebSessionTicket("opaque-reconnect-token", "", nil, nil)
	require.NoError(t, err)
	require.True(t, client.setProvisionalWebSessionTicket(ticket))

	client.Close()

	_, ok := manager.Commit(ticket, "", owner, func(string) bool { return true })
	assert.False(t, ok)
}

func TestWebSessionTicketTimerActivelyExpiresUncommittedAndCommittedStates(t *testing.T) {
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	manager := newWebSessionTicketManager()
	manager.now = func() time.Time { return now }
	callbacks := make([]func(), 0, 2)
	manager.scheduleExpiry = func(ticket string, _ time.Duration) func() bool {
		stopped := false
		callbacks = append(callbacks, func() {
			if !stopped {
				manager.expire(ticket)
			}
		})
		return func() bool {
			wasActive := !stopped
			stopped = true
			return wasActive
		}
	}

	rollbacks := 0
	issueTestWebSessionTicket(t, manager, "successor", "predecessor", func() bool {
		rollbacks++
		return true
	}, nil)
	require.Len(t, callbacks, 1)
	now = now.Add(webSessionTicketTTL)
	callbacks[0]()
	assert.Equal(t, 1, rollbacks)
	assert.Empty(t, manager.entries)
	assert.Empty(t, manager.predecessorTickets)
	assert.Empty(t, manager.successorTickets)

	now = now.Add(time.Second)
	orphans := 0
	ticket, owner := issueTestWebSessionTicket(t, manager, "committed-successor", "committed-predecessor", nil, func() bool {
		orphans++
		return true
	})
	_, ok := manager.Commit(ticket, "committed-predecessor", owner, func(string) bool { return true })
	require.True(t, ok)
	require.Len(t, callbacks, 3, "commit installs a bounded successor-observation timer")
	now = now.Add(webSessionTicketTTL)
	callbacks[2]()
	assert.Equal(t, 1, orphans)
	assert.Empty(t, manager.entries)
	assert.Empty(t, manager.predecessorTickets)
	assert.Empty(t, manager.successorTickets)
}

func TestWebSessionTicketCallbacksRunAfterManagerUnlock(t *testing.T) {
	manager := newWebSessionTicketManager()
	callbackFinished := make(chan struct{})
	ticket, _ := issueTestWebSessionTicket(t, manager, "successor", "predecessor", func() bool {
		manager.PurgeExpired()
		close(callbackFinished)
		return true
	}, nil)

	done := make(chan struct{})
	go func() {
		manager.Invalidate(ticket)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ticket invalidation deadlocked while callback re-entered manager")
	}
	select {
	case <-callbackFinished:
	default:
		t.Fatal("rollback callback did not run")
	}
}

func TestFreshOwnerNonceIsPerHandshakeAndCannotRedeemAnotherTicket(t *testing.T) {
	manager := newWebSessionTicketManager()
	manager.ownerReader = strings.NewReader(strings.Repeat("a", 32) + strings.Repeat("b", 32))
	attackerOwner, err := manager.AcquireOwnerNonce()
	require.NoError(t, err)
	victimOwner, err := manager.AcquireOwnerNonce()
	require.NoError(t, err)
	require.NotEqual(t, attackerOwner, victimOwner)

	ticket, err := manager.Issue(
		"victim-successor", "", victimOwner,
		func() bool { return true }, nil, nil, nil,
	)
	require.NoError(t, err)
	_, ok := manager.Commit(ticket, "", attackerOwner, func(string) bool { return true })
	assert.False(t, ok)
	token, ok := manager.Commit(ticket, "", victimOwner, func(string) bool { return true })
	assert.True(t, ok)
	assert.Equal(t, "victim-successor", token)
}

func TestOwnerRevokeInvalidatesInitialTicketBeforeCommit(t *testing.T) {
	manager := newWebSessionTicketManager()
	ticket, owner := issueTestWebSessionTicket(t, manager, "successor", "", nil, nil)
	revoked := make([]string, 0, 1)
	assert.Equal(t, 1, manager.InvalidatePendingOwner(owner, func(token string) {
		revoked = append(revoked, token)
	}))
	assert.Equal(t, []string{"successor"}, revoked)
	_, ok := manager.Commit(ticket, "", owner, func(string) bool { return true })
	assert.False(t, ok)
	assert.Empty(t, manager.entries)
	assert.Empty(t, manager.ownerTickets)
	assert.Empty(t, manager.successorTickets)
}

func TestPreCommitTicketInvalidationImmediatelyReleasesOwnerNonceCapacity(t *testing.T) {
	manager := newWebSessionTicketManager()
	manager.ownerReader = strings.NewReader(strings.Repeat("a", 32) + strings.Repeat("b", 32))
	owner, err := manager.AcquireOwnerNonce()
	require.NoError(t, err)
	ticket, err := manager.Issue(
		"successor", "", owner,
		func() bool { return true }, nil, nil, nil,
	)
	require.NoError(t, err)

	manager.mu.Lock()
	for i := 0; i < maxWebSessionTickets-1; i++ {
		manager.ownerNonces[fmt.Sprintf("capacity-%d", i)] = time.Now().Add(time.Hour)
	}
	manager.mu.Unlock()
	_, err = manager.AcquireOwnerNonce()
	require.ErrorIs(t, err, errWebSessionTicketCapacity)

	require.True(t, manager.Invalidate(ticket))
	manager.mu.Lock()
	_, ownerRetained := manager.ownerNonces[owner]
	_, timerRetained := manager.ownerExpiryStops[owner]
	manager.mu.Unlock()
	assert.False(t, ownerRetained)
	assert.False(t, timerRetained)

	replacement, err := manager.AcquireOwnerNonce()
	require.NoError(t, err, "the released slot must be reusable immediately")
	manager.ReleaseOwnerNonce(replacement)
}

func TestWebSessionCookieAttributes(t *testing.T) {
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	cookie := webSessionCookie(strings.Repeat("a", 64), true, now)
	assert.Equal(t, webSessionCookieName, cookie.Name)
	assert.Equal(t, "/", cookie.Path)
	assert.True(t, cookie.HttpOnly)
	assert.True(t, cookie.Secure)
	assert.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	assert.Equal(t, int((7*24*time.Hour)/time.Second), cookie.MaxAge)
	assert.Equal(t, now.Add(7*24*time.Hour), cookie.Expires)
}

func TestRequestUsesHTTPSOnlyTrustsExactForwardedProtoFromDirectProxy(t *testing.T) {
	resolver, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	require.NoError(t, err)

	tests := []struct {
		name       string
		remoteAddr string
		proto      string
		tls        bool
		want       bool
	}{
		{name: "direct TLS", remoteAddr: "203.0.113.1:1234", tls: true, want: true},
		{name: "trusted proxy", remoteAddr: "10.0.0.4:1234", proto: "https", want: true},
		{name: "untrusted spoof", remoteAddr: "203.0.113.1:1234", proto: "https", want: false},
		{name: "comma chain rejected", remoteAddr: "10.0.0.4:1234", proto: "https,http", want: false},
		{name: "case variant rejected", remoteAddr: "10.0.0.4:1234", proto: "HTTPS", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://example.test/session/commit", http.NoBody)
			r.RemoteAddr = test.remoteAddr
			r.Header.Set("X-Forwarded-Proto", test.proto)
			if test.tls {
				r.TLS = &tls.ConnectionState{}
			}
			assert.Equal(t, test.want, requestUsesHTTPS(r, resolver))
		})
	}

	duplicate := httptest.NewRequest(http.MethodPost, "http://example.test/session/commit", http.NoBody)
	duplicate.RemoteAddr = "10.0.0.4:1234"
	duplicate.Header.Add("X-Forwarded-Proto", "https")
	duplicate.Header.Add("X-Forwarded-Proto", "https")
	assert.False(t, requestUsesHTTPS(duplicate, resolver), "duplicate scheme assertions must be rejected")
}

func TestReadWebSessionCookieAcceptsOnlyOpaqueSessionTokenShape(t *testing.T) {
	valid := strings.Repeat("ab", 32)
	r := httptest.NewRequest(http.MethodGet, "http://example.test/ws", http.NoBody)
	r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: valid})
	assert.Equal(t, valid, readWebSessionCookie(r))

	r = httptest.NewRequest(http.MethodGet, "http://example.test/ws", http.NoBody)
	r.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: "player-id:" + valid})
	assert.Empty(t, readWebSessionCookie(r))
}

func TestBrowserOriginRequiresNonEmptyAllowedOrigin(t *testing.T) {
	checker := NewOriginChecker([]string{"https://game.example"})
	r := httptest.NewRequest(http.MethodPost, "https://game.example/session/revoke", http.NoBody)
	assert.False(t, browserOriginAllowed(checker, r))

	r.Header.Set("Origin", "https://evil.example")
	assert.False(t, browserOriginAllowed(checker, r))

	r.Header.Set("Origin", "https://game.example")
	assert.True(t, browserOriginAllowed(checker, r))
}
