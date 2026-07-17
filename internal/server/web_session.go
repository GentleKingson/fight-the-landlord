package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	webSessionCookieName      = "ddz_web_session"
	webSessionOwnerCookieName = "ddz_web_session_owner"
	webSessionCookieMaxAge    = 7 * 24 * time.Hour
	webSessionTicketTTL       = 30 * time.Second
	webSessionOwnerTTL        = 2 * time.Minute
	maxWebSessionTickets      = 65536
)

var (
	errWebSessionTicketCapacity = errors.New("web session ticket capacity reached")
	errWebSessionTicketEntropy  = errors.New("web session ticket generation failed")
)

type webSessionTicketEntry struct {
	token            string
	predecessorToken string
	ownerToken       string
	expiresAt        time.Time
	validateOwner    func() bool
	confirm          func() bool
	rollback         func() bool
	orphan           func() bool
	stopExpiry       func() bool
	committed        bool
}

// webSessionTicketManager exchanges a short-lived JavaScript-visible ticket
// for the opaque reconnect credential stored in an HttpOnly cookie. Tickets
// represent one rotation and never contain or encode the credential. A bound
// commit may be retried idempotently until successor observation.
type webSessionTicketManager struct {
	entries            map[string]webSessionTicketEntry
	successorTickets   map[string]map[string]struct{}
	predecessorTickets map[string]map[string]struct{}
	ownerTickets       map[string]map[string]struct{}
	ownerNonces        map[string]time.Time
	ownerExpiryStops   map[string]func() bool
	reader             io.Reader
	ownerReader        io.Reader
	now                func() time.Time
	scheduleExpiry     func(string, time.Duration) func() bool
	expirationGuard    func() func()
	mu                 sync.Mutex
}

func (s *Server) activeWebSessionTickets() *webSessionTicketManager {
	if s == nil {
		return nil
	}
	s.webSessionTicketsOnce.Do(func() {
		if s.webSessionTickets == nil {
			s.webSessionTickets = newWebSessionTicketManager()
		}
		s.webSessionTickets.expirationGuard = func() func() {
			s.sessionAuthorityMu.Lock()
			return s.sessionAuthorityMu.Unlock
		}
	})
	return s.webSessionTickets
}

func newWebSessionTicketManager() *webSessionTicketManager {
	manager := &webSessionTicketManager{
		entries:            make(map[string]webSessionTicketEntry),
		successorTickets:   make(map[string]map[string]struct{}),
		predecessorTickets: make(map[string]map[string]struct{}),
		ownerTickets:       make(map[string]map[string]struct{}),
		ownerNonces:        make(map[string]time.Time),
		ownerExpiryStops:   make(map[string]func() bool),
		reader:             rand.Reader,
		ownerReader:        rand.Reader,
		now:                time.Now,
	}
	manager.scheduleExpiry = func(ticket string, ttl time.Duration) func() bool {
		timer := time.AfterFunc(ttl, func() { manager.expire(ticket) })
		return timer.Stop
	}
	return manager
}

// AcquireOwnerNonce always creates a fresh, server-registered binding for one
// opening handshake. Presented cookies are never reused, preventing fixation.
func (manager *webSessionTicketManager) AcquireOwnerNonce() (string, error) {
	if manager == nil {
		return "", errWebSessionTicketEntropy
	}
	manager.mu.Lock()
	if len(manager.ownerNonces) >= maxWebSessionTickets {
		manager.mu.Unlock()
		return "", errWebSessionTicketCapacity
	}
	manager.mu.Unlock()

	for range 16 {
		nonce, err := newWebSessionOwnerToken(manager.ownerReader)
		if err != nil {
			return "", err
		}
		manager.mu.Lock()
		if _, exists := manager.ownerNonces[nonce]; exists {
			manager.mu.Unlock()
			continue
		}
		if len(manager.ownerNonces) >= maxWebSessionTickets {
			manager.mu.Unlock()
			return "", errWebSessionTicketCapacity
		}
		manager.ownerNonces[nonce] = manager.now().Add(webSessionOwnerTTL)
		timer := time.AfterFunc(webSessionOwnerTTL, func() { manager.expireOwnerNonce(nonce) })
		manager.ownerExpiryStops[nonce] = timer.Stop
		manager.mu.Unlock()
		return nonce, nil
	}
	return "", errWebSessionTicketEntropy
}

func (manager *webSessionTicketManager) ReleaseOwnerNonce(nonce string) {
	if manager == nil || nonce == "" {
		return
	}
	manager.mu.Lock()
	if len(manager.ownerTickets[nonce]) == 0 {
		manager.deleteOwnerNonceLocked(nonce)
	}
	manager.mu.Unlock()
}

func (manager *webSessionTicketManager) expireOwnerNonce(nonce string) {
	manager.mu.Lock()
	if expiresAt, ok := manager.ownerNonces[nonce]; ok && !manager.now().Before(expiresAt) {
		manager.deleteOwnerNonceLocked(nonce)
	}
	manager.mu.Unlock()
}

func (manager *webSessionTicketManager) deleteOwnerNonceLocked(nonce string) {
	if nonce == "" {
		return
	}
	delete(manager.ownerNonces, nonce)
	if stop := manager.ownerExpiryStops[nonce]; stop != nil {
		stop()
	}
	delete(manager.ownerExpiryStops, nonce)
}

func (manager *webSessionTicketManager) Issue(
	token string,
	predecessorToken string,
	ownerToken string,
	validateOwner func() bool,
	confirm func() bool,
	rollback func() bool,
	orphan func() bool,
) (string, error) {
	if manager == nil || token == "" || (predecessorToken == "" && ownerToken == "") {
		return "", errWebSessionTicketEntropy
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.entries) >= maxWebSessionTickets {
		return "", errWebSessionTicketCapacity
	}
	if predecessorToken == "" && !manager.ownerNonceActiveLocked(ownerToken) {
		return "", errWebSessionTicketEntropy
	}
	ticket, err := manager.generateUniqueTicketLocked()
	if err != nil {
		return "", err
	}
	entry := webSessionTicketEntry{
		token:            token,
		predecessorToken: predecessorToken,
		ownerToken:       ownerToken,
		expiresAt:        manager.now().Add(webSessionTicketTTL),
		validateOwner:    validateOwner,
		confirm:          confirm,
		rollback:         rollback,
		orphan:           orphan,
	}
	if manager.scheduleExpiry != nil {
		entry.stopExpiry = manager.scheduleExpiry(ticket, webSessionTicketTTL)
	}
	manager.entries[ticket] = entry
	manager.indexTicketLocked(ticket, entry)
	return ticket, nil
}

func (manager *webSessionTicketManager) ownerNonceActiveLocked(ownerToken string) bool {
	expiresAt, issued := manager.ownerNonces[ownerToken]
	return issued && manager.now().Before(expiresAt)
}

func (manager *webSessionTicketManager) generateUniqueTicketLocked() (string, error) {
	for range 16 {
		bytes := make([]byte, 32)
		if _, err := io.ReadFull(manager.reader, bytes); err != nil {
			return "", errors.Join(errWebSessionTicketEntropy, err)
		}
		ticket := hex.EncodeToString(bytes)
		if _, exists := manager.entries[ticket]; !exists {
			return ticket, nil
		}
	}
	return "", errWebSessionTicketEntropy
}

func (manager *webSessionTicketManager) indexTicketLocked(ticket string, entry webSessionTicketEntry) {
	if manager.successorTickets[entry.token] == nil {
		manager.successorTickets[entry.token] = make(map[string]struct{})
	}
	manager.successorTickets[entry.token][ticket] = struct{}{}
	if entry.predecessorToken != "" {
		if manager.predecessorTickets[entry.predecessorToken] == nil {
			manager.predecessorTickets[entry.predecessorToken] = make(map[string]struct{})
		}
		manager.predecessorTickets[entry.predecessorToken][ticket] = struct{}{}
		return
	}
	if manager.ownerTickets[entry.ownerToken] == nil {
		manager.ownerTickets[entry.ownerToken] = make(map[string]struct{})
	}
	manager.ownerTickets[entry.ownerToken][ticket] = struct{}{}
}

// Commit enters the response-uncertainty state only when the request presents
// the exact Cookie that preceded the pending rotation. Repeating the same
// bound commit is idempotent: the predecessor is not retired until a later
// authenticated request proves that the browser stored the successor.
func (manager *webSessionTicketManager) Commit(
	ticket string,
	predecessorToken string,
	ownerToken string,
	validate func(string) bool,
) (string, bool) {
	if manager == nil || ticket == "" {
		return "", false
	}
	entry, ok := manager.lookupCommittableEntry(ticket, ownerToken)
	if !ok || !webSessionCommitBindingMatches(entry, predecessorToken, ownerToken) {
		return "", false
	}
	if entry.validateOwner == nil || !entry.validateOwner() || validate == nil || !validate(entry.token) {
		manager.Invalidate(ticket)
		return "", false
	}
	result := manager.finalizeWebSessionCommit(ticket, ownerToken)
	if result.expired {
		manager.invalidateEntry(result.entry)
		return "", false
	}
	if !result.committed {
		return "", false
	}
	return result.entry.token, true
}

func (manager *webSessionTicketManager) lookupCommittableEntry(
	ticket, ownerToken string,
) (webSessionTicketEntry, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry, ok := manager.entries[ticket]
	if !ok {
		return webSessionTicketEntry{}, false
	}
	if !entry.committed && entry.predecessorToken == "" && !manager.ownerNonceActiveLocked(ownerToken) {
		return webSessionTicketEntry{}, false
	}
	return entry, true
}

func webSessionCommitBindingMatches(
	entry webSessionTicketEntry,
	predecessorToken, ownerToken string,
) bool {
	if entry.predecessorToken != "" {
		return constantTimeCredentialEqual(entry.predecessorToken, predecessorToken)
	}
	return constantTimeCredentialEqual(entry.ownerToken, ownerToken)
}

type webSessionCommitResult struct {
	entry     webSessionTicketEntry
	committed bool
	expired   bool
}

func (manager *webSessionTicketManager) finalizeWebSessionCommit(
	ticket, ownerToken string,
) webSessionCommitResult {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry, ok := manager.entries[ticket]
	if !ok {
		return webSessionCommitResult{}
	}
	if !manager.now().Before(entry.expiresAt) {
		manager.deleteEntryLocked(ticket, entry)
		return webSessionCommitResult{entry: entry, expired: true}
	}
	if !entry.committed && entry.predecessorToken == "" {
		if !manager.ownerNonceActiveLocked(ownerToken) {
			return webSessionCommitResult{}
		}
		manager.deleteOwnerNonceLocked(ownerToken)
	}
	if !entry.committed {
		manager.renewTicketExpiryLocked(ticket, &entry)
	}
	entry.committed = true
	manager.entries[ticket] = entry
	return webSessionCommitResult{entry: entry, committed: true}
}

func (manager *webSessionTicketManager) renewTicketExpiryLocked(
	ticket string,
	entry *webSessionTicketEntry,
) {
	if entry.stopExpiry != nil {
		entry.stopExpiry()
	}
	entry.expiresAt = manager.now().Add(webSessionTicketTTL)
	if manager.scheduleExpiry != nil {
		entry.stopExpiry = manager.scheduleExpiry(ticket, webSessionTicketTTL)
		return
	}
	entry.stopExpiry = nil
}

// ObserveSuccessor retires committed ticket state after an authenticated
// request presents the successor Cookie. SessionManager resolves the token
// alias itself; this method only discards the ticket callbacks.
func (manager *webSessionTicketManager) ObserveSuccessor(token string) int {
	if manager == nil || token == "" {
		return 0
	}
	manager.mu.Lock()
	observed := 0
	confirmations := make([]webSessionTicketEntry, 0)
	for ticket := range manager.successorTickets[token] {
		entry, ok := manager.entries[ticket]
		if !ok || !entry.committed {
			continue
		}
		manager.deleteEntryLocked(ticket, entry)
		confirmations = append(confirmations, entry)
		observed++
	}
	manager.mu.Unlock()
	for _, entry := range confirmations {
		if entry.confirm != nil {
			entry.confirm()
		}
	}
	return observed
}

func (manager *webSessionTicketManager) Invalidate(ticket string) bool {
	if manager == nil || ticket == "" {
		return false
	}
	manager.mu.Lock()
	entry, ok := manager.entries[ticket]
	if !ok {
		manager.mu.Unlock()
		return false
	}
	manager.deleteEntryLocked(ticket, entry)
	manager.mu.Unlock()
	return manager.invalidateEntry(entry)
}

// Discard removes ticket bookkeeping without firing rollback/orphan callbacks.
// It is used only when a provisional identity has been intentionally superseded
// by a successfully rebound browser identity on the same physical connection.
func (manager *webSessionTicketManager) Discard(ticket string) bool {
	if manager == nil || ticket == "" {
		return false
	}
	manager.mu.Lock()
	entry, ok := manager.entries[ticket]
	if ok {
		manager.deleteEntryLocked(ticket, entry)
	}
	manager.mu.Unlock()
	return ok
}

// InvalidatePendingCredential releases any pending state owned by the exact
// non-empty Cookie being revoked. Matching either side covers logout during
// pre-commit and post-commit uncertainty. Empty predecessors are deliberately
// excluded because they are shared by every first-time browser connection.
func (manager *webSessionTicketManager) InvalidatePendingCredential(token string) int {
	if manager == nil || token == "" {
		return 0
	}
	manager.mu.Lock()
	invalidated := 0
	tickets := make(map[string]struct{})
	for ticket := range manager.predecessorTickets[token] {
		tickets[ticket] = struct{}{}
	}
	for ticket := range manager.successorTickets[token] {
		tickets[ticket] = struct{}{}
	}
	entries := make([]webSessionTicketEntry, 0, len(tickets))
	for ticket := range tickets {
		entry, ok := manager.entries[ticket]
		if !ok {
			continue
		}
		manager.deleteEntryLocked(ticket, entry)
		entries = append(entries, entry)
		invalidated++
	}
	manager.mu.Unlock()
	manager.invalidateEntries(entries)
	return invalidated
}

// InvalidatePendingOwner linearizes a logout that races the very first ticket
// commit, before a session cookie exists. Successor sessions are revoked before
// ticket callbacks can close or orphan their owning clients.
func (manager *webSessionTicketManager) InvalidatePendingOwner(
	owner string,
	beforeInvalidate func(string),
) int {
	if manager == nil || owner == "" {
		return 0
	}
	manager.mu.Lock()
	entries := make([]webSessionTicketEntry, 0, len(manager.ownerTickets[owner]))
	for ticket := range manager.ownerTickets[owner] {
		entry, ok := manager.entries[ticket]
		if !ok {
			continue
		}
		manager.deleteEntryLocked(ticket, entry)
		entries = append(entries, entry)
	}
	manager.deleteOwnerNonceLocked(owner)
	manager.mu.Unlock()
	for _, entry := range entries {
		if beforeInvalidate != nil {
			beforeInvalidate(entry.token)
		}
		manager.invalidateEntry(entry)
	}
	return len(entries)
}

func (manager *webSessionTicketManager) PurgeExpired() int {
	if manager == nil {
		return 0
	}
	manager.mu.Lock()
	entries := manager.removeExpiredLocked()
	manager.mu.Unlock()
	manager.invalidateEntries(entries)
	return len(entries)
}

func (manager *webSessionTicketManager) removeExpiredLocked() []webSessionTicketEntry {
	now := manager.now()
	removed := make([]webSessionTicketEntry, 0)
	for ticket, entry := range manager.entries {
		if !now.Before(entry.expiresAt) {
			manager.deleteEntryLocked(ticket, entry)
			removed = append(removed, entry)
		}
	}
	return removed
}

func (manager *webSessionTicketManager) expire(ticket string) {
	if manager == nil || ticket == "" {
		return
	}
	unlock := func() {}
	if manager.expirationGuard != nil {
		unlock = manager.expirationGuard()
	}
	defer unlock()
	manager.mu.Lock()
	entry, ok := manager.entries[ticket]
	if !ok || manager.now().Before(entry.expiresAt) {
		manager.mu.Unlock()
		return
	}
	manager.deleteEntryLocked(ticket, entry)
	manager.mu.Unlock()
	manager.invalidateEntry(entry)
}

func (manager *webSessionTicketManager) deleteEntryLocked(ticket string, entry webSessionTicketEntry) {
	delete(manager.entries, ticket)
	tickets := manager.successorTickets[entry.token]
	delete(tickets, ticket)
	if len(tickets) == 0 {
		delete(manager.successorTickets, entry.token)
	}
	if entry.predecessorToken != "" {
		predecessors := manager.predecessorTickets[entry.predecessorToken]
		delete(predecessors, ticket)
		if len(predecessors) == 0 {
			delete(manager.predecessorTickets, entry.predecessorToken)
		}
	} else if entry.ownerToken != "" {
		owners := manager.ownerTickets[entry.ownerToken]
		delete(owners, ticket)
		if len(owners) == 0 {
			delete(manager.ownerTickets, entry.ownerToken)
			manager.deleteOwnerNonceLocked(entry.ownerToken)
		}
	}
	if entry.stopExpiry != nil {
		entry.stopExpiry()
	}
}

func (manager *webSessionTicketManager) invalidateEntry(entry webSessionTicketEntry) bool {
	if entry.committed {
		return entry.orphan == nil || entry.orphan()
	}
	return entry.rollback == nil || entry.rollback()
}

func (manager *webSessionTicketManager) invalidateEntries(entries []webSessionTicketEntry) {
	for _, entry := range entries {
		manager.invalidateEntry(entry)
	}
}

func constantTimeCredentialEqual(left, right string) bool {
	leftDigest := sha256.Sum256([]byte(left))
	rightDigest := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}

func browserOriginAllowed(checker *OriginChecker, r *http.Request) bool {
	return checker != nil && strings.TrimSpace(r.Header.Get("Origin")) != "" && checker.Check(r)
}

func isBrowserTransportRequest(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("Origin")) != ""
}

func requestUsesHTTPS(r *http.Request, resolver *ClientIPResolver) bool {
	if r != nil && r.TLS != nil {
		return true
	}
	if r == nil || resolver == nil {
		return false
	}
	forwardedProto := r.Header.Values("X-Forwarded-Proto")
	if len(forwardedProto) != 1 || forwardedProto[0] != "https" {
		return false
	}
	_, remoteIP := remoteClientIP(r.RemoteAddr)
	return remoteIP.IsValid() && resolver.isTrusted(remoteIP)
}

func webSessionCookie(token string, secure bool, now time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     webSessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(webSessionCookieMaxAge / time.Second),
		Expires:  now.Add(webSessionCookieMaxAge),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

func expiredWebSessionOwnerCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     webSessionOwnerCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

func webSessionOwnerCookie(token string, secure bool, now time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     webSessionOwnerCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(webSessionOwnerTTL / time.Second),
		Expires:  now.Add(webSessionOwnerTTL),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

func newWebSessionOwnerToken(reader io.Reader) (string, error) {
	bytes := make([]byte, 32)
	if _, err := io.ReadFull(reader, bytes); err != nil {
		return "", errors.Join(errWebSessionTicketEntropy, err)
	}
	return hex.EncodeToString(bytes), nil
}

func expiredWebSessionCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     webSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

func readWebSessionCookie(r *http.Request) string {
	return readOpaqueWebCookie(r, webSessionCookieName)
}

func readWebSessionOwnerCookie(r *http.Request) string {
	return readOpaqueWebCookie(r, webSessionOwnerCookieName)
}

func readOpaqueWebCookie(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	cookie, err := r.Cookie(name)
	if err != nil || len(cookie.Value) != 64 {
		return ""
	}
	decoded, err := hex.DecodeString(cookie.Value)
	if err != nil || len(decoded) != 32 {
		return ""
	}
	return cookie.Value
}
