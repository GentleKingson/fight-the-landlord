package server

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
)

func TestSlowClientFaultMatrix(t *testing.T) {
	message := &protocol.Message{Type: protocol.MsgPing}

	t.Run("delayed consumer remains bounded and connected", func(t *testing.T) {
		server := &Server{}
		client := NewClient(server, nil)
		require.Equal(t, 256, cap(client.send), "fault test must exercise the production buffer bound")
		readPermit := make(chan struct{})
		drained := make(chan struct{})
		consumerDone := make(chan struct{})
		go func() {
			defer close(consumerDone)
			for range readPermit {
				if _, ok := <-client.send; !ok {
					return
				}
				drained <- struct{}{}
			}
		}()

		// Hold the consumer behind an explicit barrier until the queue is
		// nearly full, then permit one delayed read for every new write.
		for range cap(client.send) - 1 {
			require.NoError(t, client.SendMessage(message))
		}
		assert.Len(t, client.send, cap(client.send)-1)
		for range 8 {
			readPermit <- struct{}{}
			<-drained
			require.NoError(t, client.SendMessage(message))
		}
		for range cap(client.send) - 1 {
			readPermit <- struct{}{}
			<-drained
		}
		assert.False(t, client.isClosed())
		assert.Zero(t, server.slowClientDisconnects.Load())
		client.Close()
		close(client.send)
		close(readPermit)
		<-consumerDone
	})

	t.Run("non-reading consumer exhausts buffer and disconnects once", func(t *testing.T) {
		server := &Server{}
		client := NewClient(server, nil)
		require.Equal(t, 256, cap(client.send), "fault test must exercise the production buffer bound")
		for range cap(client.send) {
			require.NoError(t, client.SendMessage(message))
		}
		require.ErrorIs(t, client.SendMessage(message), ErrClientSendBufferFull)
		assert.True(t, client.isClosed())
		assert.ErrorIs(t, client.SendMessage(message), ErrClientClosed)
		assert.EqualValues(t, 1, server.slowClientDisconnects.Load())
		assert.True(t, errors.Is(client.SendMessage(message), ErrClientClosed))
	})
}
