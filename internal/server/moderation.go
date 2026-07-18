package server

import (
	"sync"
	"time"
)

type moderationStore struct {
	mu          sync.Mutex
	mutedUntil  map[string]time.Time
	bannedUntil map[string]time.Time
	now         func() time.Time
}

func newModerationStore() *moderationStore {
	return &moderationStore{
		mutedUntil:  make(map[string]time.Time),
		bannedUntil: make(map[string]time.Time),
		now:         time.Now,
	}
}

func (s *Server) activeModerationStore() *moderationStore {
	if s == nil {
		return nil
	}
	s.moderationOnce.Do(func() {
		if s.moderation == nil {
			s.moderation = newModerationStore()
		}
	})
	return s.moderation
}

func (store *moderationStore) clock() time.Time {
	if store.now != nil {
		return store.now()
	}
	return time.Now()
}

func (store *moderationStore) mute(playerID string, duration time.Duration) (time.Time, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.clock()
	if expiresAt, exists := store.mutedUntil[playerID]; exists && now.Before(expiresAt) {
		return expiresAt, false
	}
	expiresAt := now.Add(duration)
	store.mutedUntil[playerID] = expiresAt
	return expiresAt, true
}

func (store *moderationStore) ban(playerID string, duration time.Duration) (time.Time, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.clock()
	if expiresAt, exists := store.bannedUntil[playerID]; exists && now.Before(expiresAt) {
		return expiresAt, false
	}
	expiresAt := now.Add(duration)
	store.bannedUntil[playerID] = expiresAt
	return expiresAt, true
}

func (store *moderationStore) unmute(playerID string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, existed := store.mutedUntil[playerID]
	delete(store.mutedUntil, playerID)
	return existed
}

func (store *moderationStore) unban(playerID string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, existed := store.bannedUntil[playerID]
	delete(store.bannedUntil, playerID)
	return existed
}

func (store *moderationStore) isMuted(playerID string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return activeModerationEntry(store.mutedUntil, playerID, store.clock())
}

func (store *moderationStore) isBanned(playerID string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return activeModerationEntry(store.bannedUntil, playerID, store.clock())
}

func activeModerationEntry(entries map[string]time.Time, playerID string, now time.Time) bool {
	expiresAt, exists := entries[playerID]
	if !exists {
		return false
	}
	if !now.Before(expiresAt) {
		delete(entries, playerID)
		return false
	}
	return true
}

func (store *moderationStore) counts() (muted, banned int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.clock()
	pruneExpiredModerationEntries(store.mutedUntil, now)
	pruneExpiredModerationEntries(store.bannedUntil, now)
	return len(store.mutedUntil), len(store.bannedUntil)
}

func pruneExpiredModerationEntries(entries map[string]time.Time, now time.Time) {
	for playerID, expiresAt := range entries {
		if !now.Before(expiresAt) {
			delete(entries, playerID)
		}
	}
}

// IsPlayerMuted is consumed by the chat handler through an optional capability
// interface, keeping lightweight test servers compatible.
func (s *Server) IsPlayerMuted(playerID string) bool {
	store := s.activeModerationStore()
	return store != nil && store.isMuted(playerID)
}

// IsPlayerBanned prevents restoration of an existing player identity. A ban
// also closes any currently connected client when it is applied by admin.
func (s *Server) IsPlayerBanned(playerID string) bool {
	store := s.activeModerationStore()
	return store != nil && store.isBanned(playerID)
}
