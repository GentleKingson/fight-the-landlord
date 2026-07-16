package server

import (
	"container/list"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

const (
	defaultCommandCacheCapacity = 4096
	defaultCommandCacheTTL      = 2 * time.Minute
)

var (
	errCommandCacheFull = errors.New("command idempotency cache is full")
	errRequestConflict  = errors.New("request id was reused for a different command")
)

type commandCache struct {
	mu       sync.Mutex
	entries  map[string]*commandCacheEntry
	lru      list.List
	capacity int
	ttl      time.Duration
	now      func() time.Time
}

type commandCacheEntry struct {
	fingerprint [sha256.Size]byte
	keys        map[string]struct{}
	responses   []*protocol.Message
	done        chan struct{}
	createdAt   time.Time
	expiresAt   time.Time
	finished    bool
	element     *list.Element
}

type commandCacheLookup struct {
	entry     *commandCacheEntry
	responses []*protocol.Message
	wait      <-chan struct{}
	owner     bool
}

func newCommandCache(capacity int, ttl time.Duration) *commandCache {
	if capacity <= 0 {
		capacity = defaultCommandCacheCapacity
	}
	if ttl <= 0 {
		ttl = defaultCommandCacheTTL
	}
	return &commandCache{
		entries:  make(map[string]*commandCacheEntry),
		capacity: capacity,
		ttl:      ttl,
		now:      time.Now,
	}
}

func (cache *commandCache) begin(playerID, requestID string, fingerprint [sha256.Size]byte) (commandCacheLookup, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.pruneExpiredLocked()

	key := commandCacheKey(playerID, requestID)
	if entry := cache.entries[key]; entry != nil {
		if entry.fingerprint != fingerprint {
			return commandCacheLookup{}, errRequestConflict
		}
		cache.lru.MoveToFront(entry.element)
		if entry.finished {
			return commandCacheLookup{entry: entry, responses: cloneCommandResponses(entry.responses)}, nil
		}
		return commandCacheLookup{entry: entry, wait: entry.done}, nil
	}

	if cache.lru.Len() >= cache.capacity && !cache.evictOldestFinishedLocked() {
		return commandCacheLookup{}, errCommandCacheFull
	}
	entry := &commandCacheEntry{
		fingerprint: fingerprint,
		keys:        map[string]struct{}{key: {}},
		done:        make(chan struct{}),
		createdAt:   cache.now(),
	}
	entry.element = cache.lru.PushFront(entry)
	cache.entries[key] = entry
	return commandCacheLookup{entry: entry, owner: true}, nil
}

func (cache *commandCache) finish(entry *commandCacheEntry, responses []*protocol.Message, aliasPlayerID, requestID string) {
	if entry == nil || len(responses) == 0 {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry.finished {
		return
	}
	entry.responses = cloneCommandResponses(responses)
	entry.expiresAt = cache.now().Add(cache.ttl)
	entry.finished = true
	if aliasPlayerID != "" {
		alias := commandCacheKey(aliasPlayerID, requestID)
		if current := cache.entries[alias]; current == nil || current == entry {
			cache.entries[alias] = entry
			entry.keys[alias] = struct{}{}
		}
	}
	cache.lru.MoveToFront(entry.element)
	close(entry.done)
}

func (cache *commandCache) responsesAfter(entry *commandCacheEntry) []*protocol.Message {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry == nil || !entry.finished {
		return nil
	}
	cache.lru.MoveToFront(entry.element)
	return cloneCommandResponses(entry.responses)
}

// abort releases duplicate waiters without manufacturing a successful result.
// The caller deliberately lets the handler panic continue to unwind because
// authoritative state may already have been mutated.
func (cache *commandCache) abort(entry *commandCacheEntry) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry == nil || entry.finished {
		return
	}
	cache.removeLocked(entry)
	close(entry.done)
}

func (cache *commandCache) pruneExpiredLocked() {
	now := cache.now()
	for element := cache.lru.Back(); element != nil; {
		previous := element.Prev()
		entry := element.Value.(*commandCacheEntry)
		if entry.finished && !entry.expiresAt.After(now) {
			cache.removeLocked(entry)
		}
		element = previous
	}
}

func (cache *commandCache) evictOldestFinishedLocked() bool {
	for element := cache.lru.Back(); element != nil; element = element.Prev() {
		entry := element.Value.(*commandCacheEntry)
		if entry.finished {
			cache.removeLocked(entry)
			return true
		}
	}
	return false
}

func (cache *commandCache) removeLocked(entry *commandCacheEntry) {
	for key := range entry.keys {
		if cache.entries[key] == entry {
			delete(cache.entries, key)
		}
	}
	cache.lru.Remove(entry.element)
}

func commandCacheKey(playerID, requestID string) string {
	return playerID + "\x00" + requestID
}

func commandFingerprint(msg *protocol.Message) [sha256.Size]byte {
	hash := sha256.New()
	writeFingerprintPart(hash, []byte(msg.Type))
	writeFingerprintPart(hash, msg.Payload)
	if msg.Command != nil {
		writeFingerprintPart(hash, []byte(msg.Command.ExpectedGameID))
		writeFingerprintPart(hash, []byte(strconv.FormatInt(msg.Command.ExpectedTurnID, 10)))
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

type fingerprintWriter interface {
	Write([]byte) (int, error)
}

func writeFingerprintPart(writer fingerprintWriter, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(value)
}

func cloneCommandResponses(responses []*protocol.Message) []*protocol.Message {
	clones := make([]*protocol.Message, 0, len(responses))
	for _, response := range responses {
		if response != nil {
			clones = append(clones, codec.CloneMessage(response))
		}
	}
	return clones
}
