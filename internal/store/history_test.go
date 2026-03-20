package store_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTx(method, host, url string, status int) store.Transaction {
	return store.Transaction{
		Timestamp:   time.Now().UTC(),
		Host:        host,
		Method:      method,
		URL:         url,
		ReqHeaders:  http.Header{"Content-Type": []string{"text/plain"}},
		ReqBody:     []byte("request body"),
		StatusCode:  status,
		RespHeaders: http.Header{"Content-Type": []string{"text/html"}},
		RespBody:    []byte("response body"),
		DurationMs:  10,
		TLS:         false,
		InScope:     true,
	}
}

func TestLog_Insert(t *testing.T) {
	st := newTestStore(t)

	tx := makeTx("GET", "example.com", "https://example.com/", 200)
	require.NoError(t, st.Log(tx))

	txs, err := st.AllTransactions()
	require.NoError(t, err)
	require.Len(t, txs, 1)
	assert.Equal(t, "GET", txs[0].Method)
	assert.Equal(t, "example.com", txs[0].Host)
	assert.Equal(t, 200, txs[0].StatusCode)
	assert.True(t, txs[0].InScope)
}

func TestLog_Dedup_SameMethodHostURLStatus(t *testing.T) {
	st := newTestStore(t)

	tx := makeTx("GET", "example.com", "https://example.com/", 200)
	require.NoError(t, st.Log(tx))
	require.NoError(t, st.Log(tx))

	txs, err := st.AllTransactions()
	require.NoError(t, err)
	assert.Len(t, txs, 1, "duplicate must update, not insert")
}

func TestLog_NoDedupOnDifferentStatus(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.Log(makeTx("GET", "example.com", "https://example.com/", 200)))
	require.NoError(t, st.Log(makeTx("GET", "example.com", "https://example.com/", 404)))

	txs, err := st.AllTransactions()
	require.NoError(t, err)
	assert.Len(t, txs, 2)
}

func TestLog_NoDedupOnDifferentMethod(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.Log(makeTx("GET", "example.com", "https://example.com/", 200)))
	require.NoError(t, st.Log(makeTx("POST", "example.com", "https://example.com/", 200)))

	txs, err := st.AllTransactions()
	require.NoError(t, err)
	assert.Len(t, txs, 2)
}

func TestLog_NoDedupOnDifferentURL(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.Log(makeTx("GET", "example.com", "https://example.com/a", 200)))
	require.NoError(t, st.Log(makeTx("GET", "example.com", "https://example.com/b", 200)))

	txs, err := st.AllTransactions()
	require.NoError(t, err)
	assert.Len(t, txs, 2)
}

func TestLog_TLSFlagPersisted(t *testing.T) {
	st := newTestStore(t)

	tx := makeTx("GET", "example.com", "https://example.com/", 200)
	tx.TLS = true
	require.NoError(t, st.Log(tx))

	txs, err := st.AllTransactions()
	require.NoError(t, err)
	require.Len(t, txs, 1)
	assert.True(t, txs[0].TLS)
}

func TestAllTransactions_OrderedByIDDesc(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.Log(makeTx("GET", "first.com", "https://first.com/", 200)))
	require.NoError(t, st.Log(makeTx("GET", "second.com", "https://second.com/", 200)))
	require.NoError(t, st.Log(makeTx("GET", "third.com", "https://third.com/", 200)))

	txs, err := st.AllTransactions()
	require.NoError(t, err)
	require.Len(t, txs, 3)
	assert.Equal(t, "third.com", txs[0].Host)
	assert.Equal(t, "second.com", txs[1].Host)
	assert.Equal(t, "first.com", txs[2].Host)
}

func TestAllTransactions_CappedAt100(t *testing.T) {
	st := newTestStore(t)

	for i := 0; i < 110; i++ {
		url := fmt.Sprintf("https://example.com/path/%d", i)
		require.NoError(t, st.Log(makeTx("GET", "example.com", url, 200)))
	}

	txs, err := st.AllTransactions()
	require.NoError(t, err)
	assert.LessOrEqual(t, len(txs), 100)
}

func TestGetTransaction_ReturnsFullBodies(t *testing.T) {
	st := newTestStore(t)

	tx := makeTx("POST", "example.com", "https://example.com/api", 201)
	tx.ReqBody = []byte(`{"input":"data"}`)
	tx.RespBody = []byte(`{"output":"result"}`)
	require.NoError(t, st.Log(tx))

	txs, err := st.AllTransactions()
	require.NoError(t, err)
	require.Len(t, txs, 1)

	full, err := st.GetTransaction(txs[0].ID)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"input":"data"}`), full.ReqBody)
	assert.Equal(t, []byte(`{"output":"result"}`), full.RespBody)
	assert.Equal(t, "POST", full.Method)
	assert.Equal(t, 201, full.StatusCode)
}

func TestGetTransaction_HeadersDeserialized(t *testing.T) {
	st := newTestStore(t)

	tx := makeTx("GET", "example.com", "https://example.com/", 200)
	tx.ReqHeaders = http.Header{"X-Request-Id": []string{"req-123"}}
	tx.RespHeaders = http.Header{"X-Trace-Id": []string{"trace-456"}}
	require.NoError(t, st.Log(tx))

	txs, err := st.AllTransactions()
	require.NoError(t, err)

	full, err := st.GetTransaction(txs[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "req-123", full.ReqHeaders.Get("X-Request-Id"))
	assert.Equal(t, "trace-456", full.RespHeaders.Get("X-Trace-Id"))
}

func TestGetTransaction_NotFound(t *testing.T) {
	st := newTestStore(t)
	_, err := st.GetTransaction(99999)
	assert.Error(t, err)
}

func TestClearHistory_RemovesAllRows(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.Log(makeTx("GET", "a.com", "https://a.com/", 200)))
	require.NoError(t, st.Log(makeTx("GET", "b.com", "https://b.com/", 200)))
	require.NoError(t, st.ClearHistory())

	txs, err := st.AllTransactions()
	require.NoError(t, err)
	assert.Empty(t, txs)
}

func TestClearHistory_EmptyStoreNoError(t *testing.T) {
	st := newTestStore(t)
	assert.NoError(t, st.ClearHistory())
}
