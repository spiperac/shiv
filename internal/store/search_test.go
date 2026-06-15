package store_test

import (
	"testing"
	"time"

	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func logTxWithBodies(t *testing.T, st *store.Store, method, host, url string, status int, reqBody, respBody string) {
	t.Helper()
	tx := store.Transaction{
		Timestamp:  time.Now().UTC(),
		Host:       host,
		Method:     method,
		URL:        url,
		ReqBody:    []byte(reqBody),
		RespBody:   []byte(respBody),
		StatusCode: status,
		InScope:    true,
	}
	require.NoError(t, st.Log(tx))
}

func TestSearchTransactions_FindsInRespBody(t *testing.T) {
	st := newTestStore(t)
	logTxWithBodies(t, st, "GET", "a.com", "https://a.com/", 200, "", "secret_token_here")
	logTxWithBodies(t, st, "GET", "b.com", "https://b.com/", 200, "", "nothing special")

	results, err := st.SearchTransactions("secret_token", false, true, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "a.com", results[0].Host)
}

func TestSearchTransactions_FindsInReqBody(t *testing.T) {
	st := newTestStore(t)
	logTxWithBodies(t, st, "POST", "a.com", "https://a.com/login", 200, "password=hunter2", "ok")
	logTxWithBodies(t, st, "GET", "b.com", "https://b.com/", 200, "", "ok")

	results, err := st.SearchTransactions("hunter2", true, false, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "a.com", results[0].Host)
}

func TestSearchTransactions_EmptyTerm_ReturnsNil(t *testing.T) {
	st := newTestStore(t)
	logTxWithBodies(t, st, "GET", "a.com", "https://a.com/", 200, "", "data")

	results, err := st.SearchTransactions("", true, true, false)
	require.NoError(t, err)
	assert.Nil(t, results)
}

func TestSearchTransactions_NoFieldsSelected_ReturnsNil(t *testing.T) {
	st := newTestStore(t)
	logTxWithBodies(t, st, "GET", "a.com", "https://a.com/", 200, "", "data")

	results, err := st.SearchTransactions("data", false, false, false)
	require.NoError(t, err)
	assert.Nil(t, results)
}

func TestSearchTransactions_NoMatch_ReturnsEmpty(t *testing.T) {
	st := newTestStore(t)
	logTxWithBodies(t, st, "GET", "a.com", "https://a.com/", 200, "", "nothing here")

	results, err := st.SearchTransactions("xyzzy_not_found", false, true, false)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestTransactionsPage_MethodFilter(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.Log(makeTx("GET", "a.com", "https://a.com/", 200)))
	require.NoError(t, st.Log(makeTx("POST", "a.com", "https://a.com/login", 200)))
	require.NoError(t, st.Log(makeTx("PUT", "a.com", "https://a.com/item", 200)))

	results, err := st.TransactionsPage(0, store.TransactionFilter{
		ShowOutScope: true,
		Methods:      []string{"POST"},
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "POST", results[0].Method)
}

func TestTransactionsPage_StatusRangeFilter(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.Log(makeTx("GET", "a.com", "https://a.com/ok", 200)))
	require.NoError(t, st.Log(makeTx("GET", "a.com", "https://a.com/redir", 302)))
	require.NoError(t, st.Log(makeTx("GET", "a.com", "https://a.com/err", 404)))

	results, err := st.TransactionsPage(0, store.TransactionFilter{
		ShowOutScope: true,
		StatusMin:    400,
		StatusMax:    499,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 404, results[0].StatusCode)
}
