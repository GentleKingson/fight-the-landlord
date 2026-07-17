package server

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/game/match"
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

func TestSPAHandlerServesIndexAndFallback(t *testing.T) {
	t.Parallel()

	handler := newTestSPAHandler(t)
	for _, requestPath := range []string{"/", "/room/836219", "/game/table/"} {
		t.Run(requestPath, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, requestPath, http.NoBody)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			res := w.Result()
			defer func() { _ = res.Body.Close() }()
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, res.StatusCode)
			assert.Contains(t, string(body), `<div id="root"></div>`)
			assert.Equal(t, indexCacheControl, res.Header.Get("Cache-Control"))
			assert.Equal(t, "v1.2.3", res.Header.Get("X-Web-Client-Version"))
			assert.NotEmpty(t, res.Header.Get("ETag"))
		})
	}
}

func TestSPAHandlerCachesHashedAssets(t *testing.T) {
	t.Parallel()

	handler := newTestSPAHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/app-a1b2c3.js", http.NoBody)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "console.log('ready')", string(body))
	assert.Equal(t, assetCacheControl, res.Header.Get("Cache-Control"))
	assert.Contains(t, res.Header.Get("Content-Type"), "javascript")
}

func TestSPAHandlerDoesNotFallbackForMissingFiles(t *testing.T) {
	t.Parallel()

	handler := newTestSPAHandler(t)
	for _, requestPath := range []string{"/assets/missing.js", "/favicon.ico", "/../secret.txt"} {
		t.Run(requestPath, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, requestPath, http.NoBody)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
		})
	}
}

func TestSPAHandlerHonorsIndexETag(t *testing.T) {
	t.Parallel()

	handler := newTestSPAHandler(t)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("If-None-Match", first.Header().Get("ETag"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotModified, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestSPAHandlerRejectsUnsupportedMethods(t *testing.T) {
	t.Parallel()

	handler := newTestSPAHandler(t)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body")))

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, "GET, HEAD", w.Header().Get("Allow"))
}

func TestSPAHandlerRequiresIndex(t *testing.T) {
	t.Parallel()

	_, err := newSPAHandler(fstest.MapFS{}, "dev")
	require.Error(t, err)
}

func TestSessionCommitSetsOpaqueHttpOnlyCookieAndRetiresTicketAfterRefresh(t *testing.T) {
	t.Parallel()
	manager := session.NewSessionManager()
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	playerSession := manager.MustCreateSession("player-1", "Player One")
	server := &Server{
		sessionManager:    manager,
		originChecker:     NewOriginChecker([]string{"https://game.example"}),
		webSessionTickets: newWebSessionTicketManager(),
	}
	owner, err := server.activeWebSessionTickets().AcquireOwnerNonce()
	require.NoError(t, err)
	ticket, err := server.activeWebSessionTickets().Issue(
		playerSession.ReconnectToken, "", owner,
		func() bool { return true }, nil, nil, nil,
	)
	require.NoError(t, err)

	request := httptest.NewRequest(http.MethodPost, "/session/commit", strings.NewReader(`{"ticket":"`+ticket+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://game.example")
	request.AddCookie(&http.Cookie{Name: webSessionOwnerCookieName, Value: owner})
	recorder := httptest.NewRecorder()
	server.handleSessionCommit(recorder, request)
	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Len(t, recorder.Result().Cookies(), 2)
	cookie := responseCookie(t, recorder, webSessionCookieName)
	assert.Equal(t, webSessionCookieName, cookie.Name)
	assert.Equal(t, playerSession.ReconnectToken, cookie.Value)
	assert.True(t, cookie.HttpOnly)
	assert.False(t, cookie.Secure)
	assert.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	assert.Equal(t, "/", cookie.Path)
	assert.Equal(t, int(webSessionCookieMaxAge/time.Second), cookie.MaxAge)

	refresh := httptest.NewRequest(http.MethodPost, "/session/refresh", strings.NewReader(`{}`))
	refresh.Header.Set("Content-Type", "application/json")
	refresh.Header.Set("Origin", "https://game.example")
	refresh.AddCookie(cookie)
	refreshRecorder := httptest.NewRecorder()
	server.handleSessionRefresh(refreshRecorder, refresh)
	require.Equal(t, http.StatusNoContent, refreshRecorder.Code)

	replayed := httptest.NewRequest(http.MethodPost, "/session/commit", strings.NewReader(`{"ticket":"`+ticket+`"}`))
	replayed.Header.Set("Content-Type", "application/json")
	replayed.Header.Set("Origin", "https://game.example")
	replayedRecorder := httptest.NewRecorder()
	server.handleSessionCommit(replayedRecorder, replayed)
	assert.Equal(t, http.StatusUnauthorized, replayedRecorder.Code)
}

func TestSessionCommitRequiresExactPredecessorCookieWithoutInvalidatingOwner(t *testing.T) {
	fixture := newPendingBrowserFixture(t)

	wrongCookie := strings.Repeat("f", 64)
	wrong := commitSessionTicket(fixture.server, fixture.ticket, wrongCookie)
	assert.Equal(t, http.StatusUnauthorized, wrong.Code)
	missing := commitSessionTicket(fixture.server, fixture.ticket, "")
	assert.Equal(t, http.StatusUnauthorized, missing.Code)
	assert.False(t, fixture.sessions.CanReconnectToken(fixture.predecessor))
	assert.True(t, fixture.sessions.CanReconnectToken(fixture.restored.ReconnectToken))

	owner := commitSessionTicket(fixture.server, fixture.ticket, fixture.predecessor)
	require.Equal(t, http.StatusNoContent, owner.Code)
	require.Len(t, owner.Result().Cookies(), 2)
	assert.Equal(t, fixture.restored.ReconnectToken, responseCookie(t, owner, webSessionCookieName).Value)
	fixture.client.Close()
}

func TestSessionCommitResponseLossLeavesPredecessorRecoverableAfterClose(t *testing.T) {
	fixture := newPendingBrowserFixture(t)

	// The handler changes server state and writes Set-Cookie, but the recorder is
	// deliberately discarded to model an aborted response before browser storage.
	lostResponse := commitSessionTicket(fixture.server, fixture.ticket, fixture.predecessor)
	require.Equal(t, http.StatusNoContent, lostResponse.Code)
	fixture.client.Close()
	assert.True(t, fixture.sessions.CanReconnectToken(fixture.predecessor))
	assert.True(t, fixture.sessions.CanReconnectToken(fixture.restored.ReconnectToken))

	fixture.sessions.MustCreateSession("retry-temporary", "Retry Temporary")
	retried, err := fixture.sessions.RestoreSessionByToken(fixture.predecessor, "retry-temporary")
	require.NoError(t, err)
	assert.Equal(t, fixture.restored.PlayerID, retried.PlayerID)
	_, err = fixture.sessions.RestoreSessionByToken(fixture.restored.ReconnectToken, "loser-temporary")
	assert.ErrorIs(t, err, session.ErrInvalidReconnect)
}

func TestSessionRefreshObservesSuccessorRenewsCookieAndRejectsOldReplay(t *testing.T) {
	fixture := newPendingBrowserFixture(t)
	committed := commitSessionTicket(fixture.server, fixture.ticket, fixture.predecessor)
	require.Equal(t, http.StatusNoContent, committed.Code)
	require.Len(t, committed.Result().Cookies(), 2)
	successorCookie := responseCookie(t, committed, webSessionCookieName)

	refreshed := refreshWebSession(fixture.server, successorCookie.Value)
	require.Equal(t, http.StatusNoContent, refreshed.Code)
	require.Len(t, refreshed.Result().Cookies(), 1)
	renewed := refreshed.Result().Cookies()[0]
	assert.Equal(t, successorCookie.Value, renewed.Value)
	assert.Greater(t, renewed.Expires.Unix(), time.Now().Add(6*24*time.Hour).Unix())
	assert.Empty(t, fixture.server.activeWebSessionTickets().entries)
	assert.Empty(t, fixture.server.activeWebSessionTickets().successorTickets)

	fixture.client.Close()
	assert.False(t, fixture.sessions.CanReconnectToken(fixture.predecessor))
	assert.True(t, fixture.sessions.CanReconnectToken(fixture.restored.ReconnectToken))
	_, err := fixture.sessions.RestoreSessionByToken(fixture.predecessor, "replay-temporary")
	assert.ErrorIs(t, err, session.ErrInvalidReconnect)
}

func TestConcurrentSessionCommitRetriesReturnOneSuccessorWithoutDoubleRotation(t *testing.T) {
	fixture := newPendingBrowserFixture(t)
	const attempts = 32
	results := make(chan *httptest.ResponseRecorder, attempts)
	for range attempts {
		go func() {
			results <- commitSessionTicket(fixture.server, fixture.ticket, fixture.predecessor)
		}()
	}

	for range attempts {
		result := <-results
		require.Equal(t, http.StatusNoContent, result.Code)
		require.Len(t, result.Result().Cookies(), 2)
		assert.Equal(t, fixture.restored.ReconnectToken, responseCookie(t, result, webSessionCookieName).Value)
	}
	assert.Equal(t, fixture.restored.ReconnectToken, fixture.sessions.GetSession(fixture.restored.PlayerID).ReconnectToken)
	fixture.client.Close()
}

func TestSessionCommitValidationFailureAndExpiryRestorePredecessor(t *testing.T) {
	t.Run("validation failure", func(t *testing.T) {
		fixture := newPendingBrowserFixture(t)
		fixture.client.InvalidateWebSessionTicket(fixture.ticket)
		fixture.sessions.MustCreateSession("validation-temporary", "Validation Temporary")
		pending, err := fixture.sessions.RestoreSessionByToken(fixture.predecessor, "validation-temporary")
		require.NoError(t, err)
		client := NewClient(fixture.server, nil)
		client.browserTransport = true
		client.browserReconnectToken = fixture.predecessor
		fixture.server.registerClient(client)
		t.Cleanup(client.Close)
		invalidTicket, err := client.IssueWebSessionTicket(
			strings.Repeat("d", 64),
			fixture.predecessor,
			func() bool { return fixture.sessions.RollbackRestore(pending) },
			func() bool { return fixture.sessions.OrphanBrowserRestore(pending) },
		)
		require.NoError(t, err)
		require.True(t, client.TrackWebSessionTicket(invalidTicket))

		result := commitSessionTicket(fixture.server, invalidTicket, fixture.predecessor)
		assert.Equal(t, http.StatusUnauthorized, result.Code)
		assert.True(t, fixture.sessions.CanReconnectToken(fixture.predecessor))
		assert.False(t, fixture.sessions.CanReconnectToken(pending.ReconnectToken))
	})

	t.Run("ticket expiry", func(t *testing.T) {
		fixture := newPendingBrowserFixture(t)
		now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
		fixture.server.webSessionTickets.now = func() time.Time { return now }
		// Reissue after installing the deterministic clock so the deadline is
		// independent of wall time.
		fixture.client.InvalidateWebSessionTicket(fixture.ticket)
		fixture.sessions.SetOffline(fixture.restored.PlayerID)
		fixture.sessions.MustCreateSession("expiry-temporary", "Expiry Temporary")
		restored, err := fixture.sessions.RestoreSessionByToken(fixture.predecessor, "expiry-temporary")
		require.NoError(t, err)
		client := NewClient(fixture.server, nil)
		client.browserTransport = true
		client.browserReconnectToken = fixture.predecessor
		fixture.server.registerClient(client)
		t.Cleanup(client.Close)
		ticket, err := client.IssueWebSessionTicket(
			restored.ReconnectToken,
			fixture.predecessor,
			func() bool { return fixture.sessions.RollbackRestore(restored) },
			func() bool { return fixture.sessions.OrphanBrowserRestore(restored) },
		)
		require.NoError(t, err)
		require.True(t, client.TrackWebSessionTicket(ticket))

		now = now.Add(webSessionTicketTTL)
		assert.Equal(t, 1, fixture.server.webSessionTickets.PurgeExpired())
		assert.True(t, fixture.sessions.CanReconnectToken(fixture.predecessor))
		assert.False(t, fixture.sessions.CanReconnectToken(restored.ReconnectToken))
	})
}

func TestPendingBrowserRestoreTimerClosesReboundOwnerBeforePredecessorRecovery(t *testing.T) {
	fixture := newPendingBrowserFixture(t)
	fixture.client.InvalidateWebSessionTicket(fixture.ticket)
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	fixture.server.webSessionTickets.now = func() time.Time { return now }
	var expire func()
	fixture.server.webSessionTickets.scheduleExpiry = func(ticket string, _ time.Duration) func() bool {
		active := true
		expire = func() {
			if active {
				fixture.server.webSessionTickets.expire(ticket)
			}
		}
		return func() bool {
			wasActive := active
			active = false
			return wasActive
		}
	}

	fixture.sessions.MustCreateSession("timer-temporary", "Timer Temporary")
	restored, err := fixture.sessions.RestoreSessionByToken(fixture.predecessor, "timer-temporary")
	require.NoError(t, err)
	client := NewClient(fixture.server, nil)
	client.browserTransport = true
	client.browserReconnectToken = fixture.predecessor
	fixture.server.registerClient(client)
	ticket, err := client.IssueWebSessionTicket(
		restored.ReconnectToken,
		fixture.predecessor,
		func() bool { return fixture.sessions.RollbackRestore(restored) },
		func() bool { return fixture.sessions.OrphanBrowserRestore(restored) },
	)
	require.NoError(t, err)
	require.True(t, client.TrackWebSessionTicket(ticket))
	require.NotNil(t, expire)

	now = now.Add(webSessionTicketTTL)
	expire()
	assert.True(t, client.IsClosed())
	assert.True(t, fixture.sessions.CanReconnectToken(fixture.predecessor))
	assert.False(t, fixture.sessions.CanReconnectToken(restored.ReconnectToken))

	fixture.sessions.MustCreateSession("recovery-one", "Recovery One")
	_, err = fixture.sessions.RestoreSessionByToken(fixture.predecessor, "recovery-one")
	require.NoError(t, err)
	fixture.sessions.MustCreateSession("recovery-two", "Recovery Two")
	_, err = fixture.sessions.RestoreSessionByToken(fixture.predecessor, "recovery-two")
	assert.ErrorIs(t, err, session.ErrInvalidReconnect)
}

func TestSessionRevokeCancelsPendingRotationAndRevokesBothOutcomes(t *testing.T) {
	fixture := newPendingBrowserFixture(t)
	request := newSessionJSONRequest("/session/revoke", `{}`, fixture.predecessor)
	recorder := httptest.NewRecorder()
	fixture.server.handleSessionRevoke(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.False(t, fixture.sessions.CanReconnectToken(fixture.predecessor))
	assert.False(t, fixture.sessions.CanReconnectToken(fixture.restored.ReconnectToken))
	assert.Nil(t, fixture.sessions.GetSession(fixture.restored.PlayerID))
	assert.Empty(t, fixture.server.webSessionTickets.entries)
	assert.Empty(t, fixture.server.webSessionTickets.successorTickets)
}

func TestSessionRevokeWithOnlyOwnerCookieRevokesInitialPendingSession(t *testing.T) {
	sessions := session.NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sessions.Close()) })
	server := &Server{
		sessionManager:    sessions,
		originChecker:     NewOriginChecker([]string{"https://game.example"}),
		webSessionTickets: newWebSessionTicketManager(),
		clients:           make(map[string]*Client),
	}
	owner, err := server.activeWebSessionTickets().AcquireOwnerNonce()
	require.NoError(t, err)
	client := NewClient(server, nil)
	client.browserTransport = true
	client.browserTicketOwnerToken = owner
	server.registerClient(client)
	playerSession := sessions.MustCreateSession(client.GetID(), client.GetName())
	ticket, err := client.IssueWebSessionTicket(playerSession.ReconnectToken, "", nil, nil)
	require.NoError(t, err)
	require.True(t, client.setProvisionalWebSessionTicket(ticket))

	request := newSessionJSONRequest("/session/revoke", `{}`, "")
	request.AddCookie(&http.Cookie{Name: webSessionOwnerCookieName, Value: owner})
	recorder := httptest.NewRecorder()
	server.handleSessionRevoke(recorder, request)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	assert.True(t, client.IsClosed())
	assert.Nil(t, sessions.GetSession(client.GetID()))

	commit := newSessionJSONRequest("/session/commit", `{"ticket":"`+ticket+`"}`, "")
	commit.AddCookie(&http.Cookie{Name: webSessionOwnerCookieName, Value: owner})
	commitRecorder := httptest.NewRecorder()
	server.handleSessionCommit(commitRecorder, commit)
	assert.Equal(t, http.StatusUnauthorized, commitRecorder.Code)
}

func TestSessionRevokeWaitsForAuthorizedCommandWithoutBlockingUnrelatedAuthority(t *testing.T) {
	sessions := session.NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sessions.Close()) })
	server := &Server{
		sessionManager: sessions,
		originChecker:  NewOriginChecker([]string{"https://game.example"}),
		clients:        make(map[string]*Client),
	}
	client := NewClient(server, nil)
	client.browserTransport = true
	server.registerClient(client)
	playerSession := sessions.MustCreateSession(client.GetID(), client.GetName())
	require.True(t, client.ConfirmWebSession(playerSession.ReconnectToken))
	otherClient := NewClient(server, nil)
	otherClient.browserTransport = true
	server.registerClient(otherClient)
	otherSession := sessions.MustCreateSession(otherClient.GetID(), otherClient.GetName())
	require.True(t, otherClient.ConfirmWebSession(otherSession.ReconnectToken))
	release, authorized := client.beginBrowserCommandAuthority(protocol.MsgGetOnlineCount)
	require.True(t, authorized)
	require.True(t, server.sessionAuthorityMu.TryLock(), "an executing browser command retained global authority")
	server.sessionAuthorityMu.Unlock()
	// Transport shutdown is independent from command execution. Revoke must
	// still cross the command barrier when the Client was already marked closed.
	client.Close()

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := newSessionJSONRequest("/session/revoke", `{}`, playerSession.ReconnectToken)
		recorder := httptest.NewRecorder()
		server.handleSessionRevoke(recorder, request)
		done <- recorder
	}()
	require.Eventually(t, func() bool {
		return sessions.GetSession(client.GetID()) == nil
	}, time.Second, time.Millisecond, "revoke did not cross its credential mutation boundary")

	secondRelease, secondAuthorized := client.beginBrowserCommandAuthority(protocol.MsgGetOnlineCount)
	secondRelease()
	assert.False(t, secondAuthorized)
	select {
	case <-done:
		t.Fatal("revoke returned before the already-authorized command completed")
	default:
	}
	require.Eventually(t, func() bool {
		if !server.sessionAuthorityMu.TryLock() {
			return false
		}
		server.sessionAuthorityMu.Unlock()
		return true
	}, time.Second, time.Millisecond, "exact-session command drain retained global authority")

	refreshDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		refreshDone <- refreshWebSession(server, otherSession.ReconnectToken)
	}()
	select {
	case refreshed := <-refreshDone:
		assert.Equal(t, http.StatusNoContent, refreshed.Code)
	case <-time.After(time.Second):
		t.Fatal("unrelated refresh was blocked by another session's command drain")
	}
	select {
	case <-done:
		t.Fatal("revoke returned before the already-authorized command completed")
	default:
	}
	release()
	var recorder *httptest.ResponseRecorder
	select {
	case recorder = <-done:
	case <-time.After(time.Second):
		t.Fatal("revoke did not finish after the authorized command released")
	}
	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.True(t, client.IsClosed())
	assert.Nil(t, sessions.GetSession(client.GetID()))
	_, authorized = client.beginBrowserCommandAuthority(protocol.MsgGetOnlineCount)
	assert.False(t, authorized)
}

func TestSessionRevokeWaitsForEveryCrossLineageDrain(t *testing.T) {
	for _, releaseOrder := range []string{"existing-first", "new-first"} {
		t.Run(releaseOrder, func(t *testing.T) {
			sessions := session.NewSessionManager()
			t.Cleanup(func() { require.NoError(t, sessions.Close()) })
			server := &Server{
				sessionManager: sessions,
				originChecker:  NewOriginChecker([]string{"https://game.example"}),
				clients:        make(map[string]*Client),
			}

			firstClient := NewClient(server, nil)
			firstClient.browserTransport = true
			server.registerClient(firstClient)
			firstSession := sessions.MustCreateSession(firstClient.GetID(), firstClient.GetName())
			require.True(t, firstClient.ConfirmWebSession(firstSession.ReconnectToken))
			releaseFirst, authorized := firstClient.beginBrowserCommandAuthority(protocol.MsgCreateRoom)
			require.True(t, authorized)
			firstReleased := false
			defer func() {
				if !firstReleased {
					releaseFirst()
				}
			}()

			secondClient := NewClient(server, nil)
			secondClient.browserTransport = true
			server.registerClient(secondClient)
			secondSession := sessions.MustCreateSession(secondClient.GetID(), secondClient.GetName())
			require.True(t, secondClient.ConfirmWebSession(secondSession.ReconnectToken))
			releaseSecond, authorized := secondClient.beginBrowserCommandAuthority(protocol.MsgJoinRoom)
			require.True(t, authorized)
			secondReleased := false
			defer func() {
				if !secondReleased {
					releaseSecond()
				}
			}()

			firstDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				request := newSessionJSONRequest("/session/revoke", `{}`, firstSession.ReconnectToken)
				recorder := httptest.NewRecorder()
				server.handleSessionRevoke(recorder, request)
				firstDone <- recorder
			}()
			require.Eventually(t, func() bool {
				return sessions.GetSession(firstClient.GetID()) == nil && browserRevokeDrainCount(server) == 1
			}, time.Second, time.Millisecond, "initial lineage did not publish its drain")

			combinedDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				request := newSessionJSONRequest("/session/revoke", `{}`, secondSession.ReconnectToken)
				request.AddCookie(&http.Cookie{
					Name:  webSessionOwnerCookieName,
					Value: firstSession.ReconnectToken,
				})
				recorder := httptest.NewRecorder()
				server.handleSessionRevoke(recorder, request)
				combinedDone <- recorder
			}()
			require.Eventually(t, func() bool {
				return sessions.GetSession(secondClient.GetID()) == nil && browserRevokeDrainCount(server) == 2
			}, time.Second, time.Millisecond, "cross-lineage request dropped its new drain")

			switch releaseOrder {
			case "existing-first":
				releaseFirst()
				firstReleased = true
				select {
				case firstRecorder := <-firstDone:
					assert.Equal(t, http.StatusNoContent, firstRecorder.Code)
				case <-time.After(time.Second):
					t.Fatal("initial revoke did not finish after its command released")
				}
				select {
				case <-combinedDone:
					t.Fatal("cross-lineage revoke returned before the new lineage drained")
				default:
				}
				releaseSecond()
				secondReleased = true
			case "new-first":
				releaseSecond()
				secondReleased = true
				select {
				case <-combinedDone:
					t.Fatal("cross-lineage revoke returned before the existing lineage drained")
				default:
				}
				releaseFirst()
				firstReleased = true
			}

			select {
			case combinedRecorder := <-combinedDone:
				assert.Equal(t, http.StatusNoContent, combinedRecorder.Code)
			case <-time.After(time.Second):
				t.Fatal("cross-lineage revoke did not finish after both commands released")
			}
			if releaseOrder == "new-first" {
				select {
				case firstRecorder := <-firstDone:
					assert.Equal(t, http.StatusNoContent, firstRecorder.Code)
				case <-time.After(time.Second):
					t.Fatal("initial revoke did not finish after its command released")
				}
			}
			require.Eventually(t, func() bool {
				return browserRevokeDrainCount(server) == 0
			}, time.Second, time.Millisecond, "cross-lineage drain registry leaked")
		})
	}
}

func TestBrowserReconnectAuthorityDrainsMutatorsBeforePublishingReplacement(t *testing.T) {
	for _, blockedCommand := range []protocol.MessageType{
		protocol.MsgCreateRoom,
		protocol.MsgJoinRoom,
		protocol.MsgQuickMatch,
	} {
		t.Run(string(blockedCommand), func(t *testing.T) {
			sessions := session.NewSessionManager()
			t.Cleanup(func() { require.NoError(t, sessions.Close()) })
			matcher := match.NewMatcher(match.MatcherDeps{QueueTimeout: time.Hour})
			t.Cleanup(func() { require.NoError(t, matcher.Close()) })
			server := &Server{
				sessionManager: sessions,
				originChecker:  NewOriginChecker([]string{"https://game.example"}),
				clients:        make(map[string]*Client),
			}

			oldClient := NewClient(server, nil)
			oldClient.browserTransport = true
			server.registerClient(oldClient)
			original := sessions.MustCreateSession(oldClient.GetID(), oldClient.GetName())
			predecessorToken := original.ReconnectToken
			require.True(t, oldClient.ConfirmWebSession(predecessorToken))
			releaseOldCommand, authorized := oldClient.beginBrowserCommandAuthority(blockedCommand)
			require.True(t, authorized)
			released := false
			defer func() {
				if !released {
					releaseOldCommand()
				}
			}()

			replacement := NewClient(server, nil)
			replacement.browserTransport = true
			temporaryID := replacement.GetID()
			server.registerClient(replacement)
			sessions.MustCreateSession(temporaryID, replacement.GetName())
			otherClient := NewClient(server, nil)
			otherClient.browserTransport = true
			server.registerClient(otherClient)
			otherSession := sessions.MustCreateSession(otherClient.GetID(), otherClient.GetName())
			require.True(t, otherClient.ConfirmWebSession(otherSession.ReconnectToken))

			type authorityResult struct {
				unlock func()
				ok     bool
			}
			authorityReady := make(chan authorityResult, 1)
			go func() {
				unlock, ok := server.AcquireBrowserReconnectAuthority(predecessorToken, replacement)
				authorityReady <- authorityResult{unlock: unlock, ok: ok}
			}()
			require.Eventually(t, func() bool {
				oldClient.webSessionMu.Lock()
				transitioning := oldClient.webSessionTransition
				oldClient.webSessionMu.Unlock()
				return transitioning
			}, time.Second, time.Millisecond, "reconnect did not gate the old browser generation")
			select {
			case <-authorityReady:
				t.Fatal("reconnect authority published before the old command drained")
			default:
			}
			require.Eventually(t, func() bool {
				if !server.sessionAuthorityMu.TryLock() {
					return false
				}
				server.sessionAuthorityMu.Unlock()
				return true
			}, time.Second, time.Millisecond, "reconnect command drain retained global authority")
			assert.Equal(t, http.StatusNoContent, refreshWebSession(server, otherSession.ReconnectToken).Code)
			assert.Same(t, oldClient, server.GetClientByID(original.PlayerID))
			assert.Same(t, replacement, server.GetClientByID(temporaryID))

			releaseOldCommand()
			released = true
			var authority authorityResult
			select {
			case authority = <-authorityReady:
			case <-time.After(time.Second):
				t.Fatal("reconnect authority did not acquire after the old command drained")
			}
			require.True(t, authority.ok)
			unlocked := false
			defer func() {
				if !unlocked {
					authority.unlock()
				}
			}()
			restored, err := sessions.RestoreSessionByToken(predecessorToken, temporaryID)
			require.NoError(t, err)
			previous, err := server.RebindClient(
				temporaryID, restored.PlayerID, restored.PlayerName, restored.RoomCode, replacement,
			)
			require.NoError(t, err)
			require.Same(t, oldClient, previous)
			previous.Close()
			server.RetireBrowserSessionClient(restored.PlayerID, previous)
			authority.unlock()
			unlocked = true
			require.True(t, replacement.ConfirmWebSession(restored.ReconnectToken))

			for _, staleCommand := range []protocol.MessageType{
				protocol.MsgCreateRoom,
				protocol.MsgJoinRoom,
				protocol.MsgQuickMatch,
			} {
				releaseStale, staleAuthorized := oldClient.beginBrowserCommandAuthority(staleCommand)
				if staleAuthorized {
					if staleCommand == protocol.MsgQuickMatch {
						matcher.AddToQueue(oldClient)
					} else {
						sessions.SetRoom(restored.PlayerID, "GHOST")
						oldClient.SetRoom("GHOST")
					}
				}
				releaseStale()
				assert.False(t, staleAuthorized)
			}
			assert.Same(t, replacement, server.GetClientByID(restored.PlayerID))
			assert.Empty(t, oldClient.GetRoom())
			assert.Empty(t, sessions.GetSession(restored.PlayerID).RoomCode)
			assert.Zero(t, matcher.GetQueueLength())
		})
	}
}

func TestBrowserReconnectAuthorityFailureRestoresCurrentAuthorization(t *testing.T) {
	sessions := session.NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sessions.Close()) })
	server := &Server{sessionManager: sessions, clients: make(map[string]*Client)}
	client := NewClient(server, nil)
	client.browserTransport = true
	server.registerClient(client)
	playerSession := sessions.MustCreateSession(client.GetID(), client.GetName())
	require.True(t, client.ConfirmWebSession(playerSession.ReconnectToken))

	replacement := NewClient(server, nil)
	replacement.browserTransport = true
	unlock, ok := server.AcquireBrowserReconnectAuthority(playerSession.ReconnectToken, replacement)
	require.True(t, ok)
	unlock()
	release, authorized := client.beginBrowserCommandAuthority(protocol.MsgCreateRoom)
	release()
	assert.True(t, authorized, "failed reconnect attempt left the current browser disabled")
}

func TestBrowserReconnectAuthorityRejectsCurrentGenerationWithoutLockingIt(t *testing.T) {
	sessions := session.NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sessions.Close()) })
	server := &Server{sessionManager: sessions, clients: make(map[string]*Client)}
	client := NewClient(server, nil)
	client.browserTransport = true
	server.registerClient(client)
	playerSession := sessions.MustCreateSession(client.GetID(), client.GetName())
	require.True(t, client.ConfirmWebSession(playerSession.ReconnectToken))

	unlock, ok := server.AcquireBrowserReconnectAuthority(playerSession.ReconnectToken, client)
	unlock()
	assert.False(t, ok, "the current physical generation must not reconnect to itself")
	require.True(t, server.sessionAuthorityMu.TryLock(), "self-reconnect retained global authority")
	server.sessionAuthorityMu.Unlock()
	require.True(t, client.webCommandMu.TryLock(), "self-reconnect retained the client command barrier")
	client.webCommandMu.Unlock()
	release, authorized := client.beginBrowserCommandAuthority(protocol.MsgCreateRoom)
	release()
	assert.True(t, authorized, "rejected self-reconnect revoked the current generation")
}

func TestSessionRevokeDrainsRetiredBrowserGenerationWithoutBlockingOtherLineage(t *testing.T) {
	sessions := session.NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sessions.Close()) })
	server := &Server{
		sessionManager: sessions,
		originChecker:  NewOriginChecker([]string{"https://game.example"}),
		clients:        make(map[string]*Client),
	}

	oldClient := NewClient(server, nil)
	oldClient.browserTransport = true
	server.registerClient(oldClient)
	original := sessions.MustCreateSession(oldClient.GetID(), oldClient.GetName())
	predecessorToken := original.ReconnectToken
	require.True(t, oldClient.ConfirmWebSession(predecessorToken))
	releaseOldCommand, authorized := oldClient.beginBrowserCommandAuthority(protocol.MsgGetOnlineCount)
	require.True(t, authorized)
	released := false
	defer func() {
		if !released {
			releaseOldCommand()
		}
	}()

	replacement := NewClient(server, nil)
	replacement.browserTransport = true
	temporaryID := replacement.GetID()
	server.registerClient(replacement)
	sessions.MustCreateSession(temporaryID, replacement.GetName())

	var (
		restored   *session.RestoredSession
		previous   types.ClientInterface
		restoreErr error
		rebindErr  error
	)
	server.sessionAuthorityMu.Lock()
	restored, restoreErr = sessions.RestoreSessionByToken(predecessorToken, temporaryID)
	if restoreErr == nil {
		previous, rebindErr = server.RebindClient(
			temporaryID, restored.PlayerID, restored.PlayerName, restored.RoomCode, replacement,
		)
	}
	if restoreErr == nil && rebindErr == nil && previous != nil {
		previous.Close()
		server.RetireBrowserSessionClient(restored.PlayerID, previous)
	}
	server.sessionAuthorityMu.Unlock()
	require.NoError(t, restoreErr)
	require.NoError(t, rebindErr)
	require.Same(t, oldClient, previous)
	require.True(t, replacement.ConfirmWebSession(restored.ReconnectToken))
	assert.Equal(t, 1, retiredBrowserClientCount(server, restored.PlayerID))

	otherClient := NewClient(server, nil)
	otherClient.browserTransport = true
	server.registerClient(otherClient)
	otherSession := sessions.MustCreateSession(otherClient.GetID(), otherClient.GetName())
	require.True(t, otherClient.ConfirmWebSession(otherSession.ReconnectToken))

	revokeDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := newSessionJSONRequest("/session/revoke", `{}`, restored.ReconnectToken)
		recorder := httptest.NewRecorder()
		server.handleSessionRevoke(recorder, request)
		revokeDone <- recorder
	}()
	require.Eventually(t, func() bool {
		return sessions.GetSession(restored.PlayerID) == nil
	}, time.Second, time.Millisecond, "successor revoke did not mutate the lineage")
	select {
	case <-revokeDone:
		t.Fatal("revoke returned before the retired generation command drained")
	default:
	}
	retryDone := make(chan *httptest.ResponseRecorder, 2)
	for _, retryToken := range []string{restored.ReconnectToken, predecessorToken} {
		retryToken := retryToken
		go func() {
			request := newSessionJSONRequest("/session/revoke", `{}`, retryToken)
			recorder := httptest.NewRecorder()
			server.handleSessionRevoke(recorder, request)
			retryDone <- recorder
		}()
	}
	select {
	case <-retryDone:
		t.Fatal("same-token or alias revoke retry bypassed the lineage drain")
	default:
	}
	require.Eventually(t, func() bool {
		if !server.sessionAuthorityMu.TryLock() {
			return false
		}
		server.sessionAuthorityMu.Unlock()
		return true
	}, time.Second, time.Millisecond, "retired generation drain retained global authority")
	assert.Equal(t, http.StatusNoContent, refreshWebSession(server, otherSession.ReconnectToken).Code)
	select {
	case <-retryDone:
		t.Fatal("same-token or alias revoke retry returned before the retired command drained")
	default:
	}

	releaseOldCommand()
	released = true
	var recorder *httptest.ResponseRecorder
	select {
	case recorder = <-revokeDone:
	case <-time.After(time.Second):
		t.Fatal("revoke did not finish after the retired command released")
	}
	assert.Equal(t, http.StatusNoContent, recorder.Code)
	for range 2 {
		select {
		case retryRecorder := <-retryDone:
			assert.Equal(t, http.StatusNoContent, retryRecorder.Code)
		case <-time.After(time.Second):
			t.Fatal("revoke retry did not finish with the lineage drain")
		}
	}
	assert.True(t, oldClient.IsClosed())
	assert.True(t, replacement.IsClosed())
	require.Eventually(t, func() bool {
		return retiredBrowserClientCount(server, restored.PlayerID) == 0 && browserRevokeDrainCount(server) == 0
	}, time.Second, time.Millisecond, "retired browser generation leaked after drain")
}

func TestRetiredBrowserGenerationRegistryDrainsMultipleClosedClients(t *testing.T) {
	server := &Server{}
	first := NewClient(server, nil)
	first.browserTransport = true
	second := NewClient(server, nil)
	second.browserTransport = true
	first.webCommandMu.RLock()
	second.webCommandMu.RLock()

	server.sessionAuthorityMu.Lock()
	server.RetireBrowserSessionClient("player-lineage", first)
	server.RetireBrowserSessionClient("player-lineage", second)
	server.RetireBrowserSessionClient("player-lineage", first)
	assert.Len(t, server.retiredBrowserClients["player-lineage"], 2)
	server.sessionAuthorityMu.Unlock()
	first.Close()
	second.Close()
	require.True(t, server.sessionAuthorityMu.TryLock(), "retired drains retained global authority")
	server.sessionAuthorityMu.Unlock()

	first.webCommandMu.RUnlock()
	require.Eventually(t, func() bool {
		return retiredBrowserClientCount(server, "player-lineage") == 1
	}, time.Second, time.Millisecond)
	second.webCommandMu.RUnlock()
	require.Eventually(t, func() bool {
		return retiredBrowserClientCount(server, "player-lineage") == 0
	}, time.Second, time.Millisecond)
	server.sessionAuthorityMu.RLock()
	_, retained := server.retiredBrowserClients["player-lineage"]
	server.sessionAuthorityMu.RUnlock()
	assert.False(t, retained)
}

func TestSessionRefreshAndPredecessorRevokeCannotLeaveSuccessorAlive(t *testing.T) {
	fixture := newPendingBrowserFixture(t)
	committed := commitSessionTicket(fixture.server, fixture.ticket, fixture.predecessor)
	require.Equal(t, http.StatusNoContent, committed.Code)
	successor := responseCookie(t, committed, webSessionCookieName).Value
	start := make(chan struct{})
	results := make(chan int, 2)
	go func() {
		<-start
		results <- refreshWebSession(fixture.server, successor).Code
	}()
	go func() {
		<-start
		request := newSessionJSONRequest("/session/revoke", `{}`, fixture.predecessor)
		recorder := httptest.NewRecorder()
		fixture.server.handleSessionRevoke(recorder, request)
		results <- recorder.Code
	}()
	close(start)
	first, second := <-results, <-results
	assert.Contains(t, []int{http.StatusNoContent, http.StatusUnauthorized}, first)
	assert.Contains(t, []int{http.StatusNoContent, http.StatusUnauthorized}, second)
	assert.True(t, first == http.StatusNoContent || second == http.StatusNoContent)
	assert.Nil(t, fixture.sessions.GetSession(fixture.restored.PlayerID))
	assert.False(t, fixture.sessions.CanReconnectToken(successor))
}

func TestSessionCommitSecureCookieTrustsOnlyTLSOrConfiguredDirectProxy(t *testing.T) {
	t.Parallel()
	resolver, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	require.NoError(t, err)

	tests := []struct {
		name       string
		remoteAddr string
		proto      string
		directTLS  bool
		wantSecure bool
	}{
		{name: "direct TLS", remoteAddr: "203.0.113.8:1234", directTLS: true, wantSecure: true},
		{name: "trusted TLS proxy", remoteAddr: "10.0.0.8:1234", proto: "https", wantSecure: true},
		{name: "untrusted spoof", remoteAddr: "203.0.113.8:1234", proto: "https", wantSecure: false},
		{name: "trusted proxy non-exact value", remoteAddr: "10.0.0.8:1234", proto: "https,http", wantSecure: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := session.NewSessionManager()
			t.Cleanup(func() { require.NoError(t, manager.Close()) })
			playerSession := manager.MustCreateSession("player-1", "Player One")
			server := &Server{
				sessionManager:    manager,
				originChecker:     NewOriginChecker([]string{"https://game.example"}),
				ipResolver:        resolver,
				webSessionTickets: newWebSessionTicketManager(),
			}
			owner, ownerErr := server.activeWebSessionTickets().AcquireOwnerNonce()
			require.NoError(t, ownerErr)
			ticket, issueErr := server.activeWebSessionTickets().Issue(
				playerSession.ReconnectToken, "", owner,
				func() bool { return true }, nil, nil, nil,
			)
			require.NoError(t, issueErr)
			request := httptest.NewRequest(http.MethodPost, "/session/commit", strings.NewReader(`{"ticket":"`+ticket+`"}`))
			request.RemoteAddr = test.remoteAddr
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://game.example")
			request.AddCookie(&http.Cookie{Name: webSessionOwnerCookieName, Value: owner})
			request.Header.Set("X-Forwarded-Proto", test.proto)
			if test.directTLS {
				request.TLS = &tls.ConnectionState{}
			}
			recorder := httptest.NewRecorder()
			server.handleSessionCommit(recorder, request)
			require.Equal(t, http.StatusNoContent, recorder.Code)
			require.Len(t, recorder.Result().Cookies(), 2)
			assert.Equal(t, test.wantSecure, responseCookie(t, recorder, webSessionCookieName).Secure)
		})
	}
}

func TestSessionRevokeUsesCookieRequiresAllowedOriginAndIsIdempotent(t *testing.T) {
	t.Parallel()
	manager := session.NewSessionManager()
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	playerSession := manager.MustCreateSession("player-1", "Player One")
	server := &Server{
		sessionManager: manager,
		originChecker:  NewOriginChecker([]string{"https://game.example"}),
	}

	badOrigin := httptest.NewRequest(http.MethodPost, "/session/revoke", strings.NewReader(`{}`))
	badOrigin.Header.Set("Content-Type", "application/json")
	badOrigin.Header.Set("Origin", "https://evil.example")
	badOrigin.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: playerSession.ReconnectToken})
	badOriginRecorder := httptest.NewRecorder()
	server.handleSessionRevoke(badOriginRecorder, badOrigin)
	assert.Equal(t, http.StatusForbidden, badOriginRecorder.Code)
	assert.True(t, manager.CanReconnectToken(playerSession.ReconnectToken))

	request := httptest.NewRequest(http.MethodPost, "/session/revoke", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://game.example")
	request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: playerSession.ReconnectToken})
	recorder := httptest.NewRecorder()
	server.handleSessionRevoke(recorder, request)
	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	assert.False(t, manager.CanReconnectToken(playerSession.ReconnectToken))
	require.Len(t, recorder.Result().Cookies(), 2)
	assert.Equal(t, -1, responseCookie(t, recorder, webSessionCookieName).MaxAge)

	replayed := httptest.NewRequest(http.MethodPost, "/session/revoke", strings.NewReader(`{}`))
	replayed.Header.Set("Content-Type", "application/json")
	replayed.Header.Set("Origin", "https://game.example")
	replayed.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: playerSession.ReconnectToken})
	replayedRecorder := httptest.NewRecorder()
	server.handleSessionRevoke(replayedRecorder, replayed)
	assert.Equal(t, http.StatusNoContent, replayedRecorder.Code)
}

func TestSessionEndpointsRejectCrossSiteFormMissingOriginAndUnsupportedMethod(t *testing.T) {
	t.Parallel()
	server := &Server{originChecker: NewOriginChecker([]string{"https://game.example"})}

	form := httptest.NewRequest(http.MethodPost, "/session/revoke", strings.NewReader("player_id=p1&token=secret"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	form.Header.Set("Origin", "https://game.example")
	formRecorder := httptest.NewRecorder()
	server.handleSessionRevoke(formRecorder, form)
	assert.Equal(t, http.StatusUnsupportedMediaType, formRecorder.Code)

	missingOrigin := httptest.NewRequest(http.MethodPost, "/session/revoke", strings.NewReader(`{}`))
	missingOrigin.Header.Set("Content-Type", "application/json")
	missingOriginRecorder := httptest.NewRecorder()
	server.handleSessionRevoke(missingOriginRecorder, missingOrigin)
	assert.Equal(t, http.StatusForbidden, missingOriginRecorder.Code)

	getRecorder := httptest.NewRecorder()
	server.handleSessionRevoke(getRecorder, httptest.NewRequest(http.MethodGet, "/session/revoke", http.NoBody))
	assert.Equal(t, http.StatusMethodNotAllowed, getRecorder.Code)
	assert.Equal(t, http.MethodPost, getRecorder.Header().Get("Allow"))
}

func TestSessionEndpointsRejectUnknownAndOversizedJSON(t *testing.T) {
	t.Parallel()
	server := &Server{originChecker: NewOriginChecker([]string{"https://game.example"})}

	for _, test := range []struct {
		name string
		path string
		body string
	}{
		{name: "unknown revoke field", path: "/session/revoke", body: `{"token":"must-not-be-read"}`},
		{name: "oversized revoke", path: "/session/revoke", body: `{"padding":"` + strings.Repeat("x", 2048) + `"}`},
		{name: "oversized commit", path: "/session/commit", body: `{"ticket":"` + strings.Repeat("x", 2048) + `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://game.example")
			recorder := httptest.NewRecorder()
			if test.path == "/session/commit" {
				server.handleSessionCommit(recorder, request)
			} else {
				server.handleSessionRevoke(recorder, request)
			}
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

func newTestSPAHandler(t *testing.T) http.Handler {
	t.Helper()
	assets := fstest.MapFS{
		"index.html":           {Data: []byte(`<html><body><div id="root"></div></body></html>`)},
		"assets/app-a1b2c3.js": {Data: []byte("console.log('ready')")},
	}
	handler, err := newSPAHandler(assets, "v1.2.3")
	require.NoError(t, err)
	return handler
}

type pendingBrowserFixture struct {
	server      *Server
	sessions    *session.SessionManager
	client      *Client
	restored    *session.RestoredSession
	predecessor string
	ticket      string
}

func newPendingBrowserFixture(t *testing.T) *pendingBrowserFixture {
	t.Helper()
	sessions := session.NewSessionManager()
	t.Cleanup(func() { require.NoError(t, sessions.Close()) })
	original := sessions.MustCreateSession("browser-player", "Browser Player")
	predecessor := original.ReconnectToken
	sessions.SetOffline(original.PlayerID)
	sessions.MustCreateSession("browser-temporary", "Browser Temporary")
	restored, err := sessions.RestoreSessionByToken(predecessor, "browser-temporary")
	require.NoError(t, err)

	server := &Server{
		sessionManager:    sessions,
		originChecker:     NewOriginChecker([]string{"https://game.example"}),
		webSessionTickets: newWebSessionTicketManager(),
		clients:           make(map[string]*Client),
	}
	client := NewClient(server, nil)
	client.browserTransport = true
	client.browserReconnectToken = predecessor
	server.registerClient(client)
	ticket, err := client.IssueWebSessionTicket(
		restored.ReconnectToken,
		predecessor,
		func() bool { return sessions.RollbackRestore(restored) },
		func() bool { return sessions.OrphanBrowserRestore(restored) },
	)
	require.NoError(t, err)
	require.True(t, client.TrackWebSessionTicket(ticket))
	t.Cleanup(func() {
		client.Close()
		server.unregisterClient(client)
	})
	return &pendingBrowserFixture{
		server:      server,
		sessions:    sessions,
		client:      client,
		restored:    restored,
		predecessor: predecessor,
		ticket:      ticket,
	}
}

func commitSessionTicket(server *Server, ticket, cookie string) *httptest.ResponseRecorder {
	request := newSessionJSONRequest(
		"/session/commit",
		`{"ticket":"`+ticket+`"}`,
		cookie,
	)
	recorder := httptest.NewRecorder()
	server.handleSessionCommit(recorder, request)
	return recorder
}

func refreshWebSession(server *Server, cookie string) *httptest.ResponseRecorder {
	request := newSessionJSONRequest("/session/refresh", `{}`, cookie)
	recorder := httptest.NewRecorder()
	server.handleSessionRefresh(recorder, request)
	return recorder
}

func newSessionJSONRequest(requestPath, body, cookie string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, requestPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://game.example")
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: cookie})
	}
	return request
}

func responseCookie(t *testing.T, recorder *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	require.FailNow(t, "response cookie not found", name)
	return nil
}

func retiredBrowserClientCount(server *Server, playerID string) int {
	server.sessionAuthorityMu.RLock()
	defer server.sessionAuthorityMu.RUnlock()
	return len(server.retiredBrowserClients[playerID])
}

func browserRevokeDrainCount(server *Server) int {
	server.sessionAuthorityMu.RLock()
	defer server.sessionAuthorityMu.RUnlock()
	return len(server.browserRevokeDrains)
}
