package match

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/palemoky/fight-the-landlord/internal/testutil"
)

func TestMatcher_QueueOps(t *testing.T) {
	// As long as we keep queue size < 3, it won't call CreateRoom.
	matcher := NewMatcher(MatcherDeps{}) // nil dependencies for testing

	c1 := &testutil.SimpleClient{ID: "p1", Name: "Player1"}
	c2 := &testutil.SimpleClient{ID: "p2", Name: "Player2"}

	// Add c1
	assert.True(t, matcher.AddToQueue(c1))
	assert.Equal(t, 1, matcher.GetQueueLength())

	// Add c1 again (should be ignored)
	assert.False(t, matcher.AddToQueue(c1))
	assert.Equal(t, 1, matcher.GetQueueLength())

	// Add c2
	assert.True(t, matcher.AddToQueue(c2))
	assert.Equal(t, 2, matcher.GetQueueLength())

	// Remove c1
	assert.True(t, matcher.RemoveFromQueue(c1))
	assert.Equal(t, 1, matcher.GetQueueLength())

	// Remove c1 again (should be no-op)
	assert.False(t, matcher.RemoveFromQueue(c1))
	assert.Equal(t, 1, matcher.GetQueueLength())

	// Remove c2
	assert.True(t, matcher.RemoveFromQueue(c2))
	assert.Equal(t, 0, matcher.GetQueueLength())
}

func TestMatcher_ConcurrentDuplicateAdd(t *testing.T) {
	matcher := NewMatcher(MatcherDeps{})
	client := &testutil.SimpleClient{ID: "p1", Name: "Player1"}

	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if matcher.AddToQueue(client) {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.EqualValues(t, 1, accepted.Load())
	assert.Equal(t, 1, matcher.GetQueueLength())
	assert.True(t, matcher.RemoveFromQueue(client))
	assert.False(t, matcher.RemoveFromQueue(client))
}
