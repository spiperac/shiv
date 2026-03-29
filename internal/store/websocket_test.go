package store_test

import (
	"testing"
	"time"

	"github.com/shiv/internal/events"
	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogWebSocketConnection_PersistsAllFields(t *testing.T) {
	st := newTestStore(t)

	conn := store.WebSocketConnection{
		Host:      "example.com:443",
		URL:       "wss://example.com:443/chat",
		TLS:       true,
		InScope:   true,
		Timestamp: time.Now().UTC().Truncate(time.Second),
	}
	id, err := st.LogWebSocketConnection(conn)
	require.NoError(t, err)
	assert.NotZero(t, id)

	conns, err := st.AllWebSocketConnections()
	require.NoError(t, err)
	require.Len(t, conns, 1)
	assert.Equal(t, id, conns[0].ID)
	assert.Equal(t, "example.com:443", conns[0].Host)
	assert.Equal(t, "wss://example.com:443/chat", conns[0].URL)
	assert.True(t, conns[0].TLS)
	assert.True(t, conns[0].InScope)
}

func TestLogWebSocketFrame_PersistsAllFields(t *testing.T) {
	st := newTestStore(t)

	connID, err := st.LogWebSocketConnection(store.WebSocketConnection{
		Host:      "example.com:443",
		URL:       "wss://example.com:443/",
		TLS:       true,
		Timestamp: time.Now().UTC(),
	})
	require.NoError(t, err)

	frame := store.WebSocketFrame{
		ConnectionID: connID,
		Timestamp:    time.Now().UTC().Truncate(time.Second),
		Direction:    events.WebSocketClient,
		Opcode:       events.WebSocketText,
		Payload:      []byte("hello world"),
	}
	require.NoError(t, st.LogWebSocketFrame(frame))

	frames, err := st.FramesForConnection(connID)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	assert.Equal(t, connID, frames[0].ConnectionID)
	assert.Equal(t, events.WebSocketClient, frames[0].Direction)
	assert.Equal(t, events.WebSocketText, frames[0].Opcode)
	assert.Equal(t, []byte("hello world"), frames[0].Payload)
}

func TestAllWebSocketConnections_OrderedByIDDesc(t *testing.T) {
	st := newTestStore(t)

	for _, host := range []string{"first.com:443", "second.com:443", "third.com:443"} {
		_, err := st.LogWebSocketConnection(store.WebSocketConnection{
			Host:      host,
			URL:       "wss://" + host + "/",
			Timestamp: time.Now().UTC(),
		})
		require.NoError(t, err)
	}

	conns, err := st.AllWebSocketConnections()
	require.NoError(t, err)
	require.Len(t, conns, 3)
	assert.Equal(t, "third.com:443", conns[0].Host)
	assert.Equal(t, "second.com:443", conns[1].Host)
	assert.Equal(t, "first.com:443", conns[2].Host)
}

func TestAllWebSocketConnections_FrameCountIsCorrect(t *testing.T) {
	st := newTestStore(t)

	connID, err := st.LogWebSocketConnection(store.WebSocketConnection{
		Host:      "example.com:443",
		URL:       "wss://example.com:443/",
		Timestamp: time.Now().UTC(),
	})
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		require.NoError(t, st.LogWebSocketFrame(store.WebSocketFrame{
			ConnectionID: connID,
			Timestamp:    time.Now().UTC(),
			Direction:    events.WebSocketClient,
			Opcode:       events.WebSocketText,
			Payload:      []byte("msg"),
		}))
	}

	conns, err := st.AllWebSocketConnections()
	require.NoError(t, err)
	require.Len(t, conns, 1)
	assert.Equal(t, 5, conns[0].FrameCount)
}

func TestFramesForConnection_OrderedByIDAsc(t *testing.T) {
	st := newTestStore(t)

	connID, err := st.LogWebSocketConnection(store.WebSocketConnection{
		Host:      "example.com:443",
		URL:       "wss://example.com:443/",
		Timestamp: time.Now().UTC(),
	})
	require.NoError(t, err)

	directions := []events.WebSocketDirection{
		events.WebSocketClient,
		events.WebSocketServer,
		events.WebSocketClient,
	}
	for _, dir := range directions {
		require.NoError(t, st.LogWebSocketFrame(store.WebSocketFrame{
			ConnectionID: connID,
			Timestamp:    time.Now().UTC(),
			Direction:    dir,
			Opcode:       events.WebSocketText,
			Payload:      []byte("x"),
		}))
	}

	frames, err := st.FramesForConnection(connID)
	require.NoError(t, err)
	require.Len(t, frames, 3)
	assert.Equal(t, events.WebSocketClient, frames[0].Direction)
	assert.Equal(t, events.WebSocketServer, frames[1].Direction)
	assert.Equal(t, events.WebSocketClient, frames[2].Direction)
}

func TestFramesForConnection_EmptyForUnknownConnection(t *testing.T) {
	st := newTestStore(t)
	frames, err := st.FramesForConnection(99999)
	require.NoError(t, err)
	assert.Empty(t, frames)
}

func TestLogWebSocketConnection_EmitsToChannel(t *testing.T) {
	st := newTestStore(t)

	conn := store.WebSocketConnection{
		Host:      "example.com:443",
		URL:       "wss://example.com:443/",
		Timestamp: time.Now().UTC(),
	}
	_, err := st.LogWebSocketConnection(conn)
	require.NoError(t, err)

	select {
	case c := <-st.WebSocketConnections:
		assert.Equal(t, "example.com:443", c.Host)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for WebSocketConnections channel emit")
	}
}

func TestLogWebSocketFrame_EmitsToChannel(t *testing.T) {
	st := newTestStore(t)

	connID, err := st.LogWebSocketConnection(store.WebSocketConnection{
		Host:      "example.com:443",
		URL:       "wss://example.com:443/",
		Timestamp: time.Now().UTC(),
	})
	require.NoError(t, err)
	// Drain the connection channel.
	<-st.WebSocketConnections

	err = st.LogWebSocketFrame(store.WebSocketFrame{
		ConnectionID: connID,
		Timestamp:    time.Now().UTC(),
		Direction:    events.WebSocketServer,
		Opcode:       events.WebSocketText,
		Payload:      []byte("streamed"),
	})
	require.NoError(t, err)

	select {
	case f := <-st.WebSocketFrames:
		assert.Equal(t, connID, f.ConnectionID)
		assert.Equal(t, events.WebSocketServer, f.Direction)
		assert.Equal(t, []byte("streamed"), f.Payload)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for WebSocketFrames channel emit")
	}
}
