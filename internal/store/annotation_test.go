package store_test

import (
	"testing"

	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func logTxForAnnotation(t *testing.T, st *store.Store) uint64 {
	t.Helper()
	tx := makeTx("GET", "example.com", "https://example.com/", 200)
	require.NoError(t, st.Log(tx))
	txs, err := st.AllTransactions()
	require.NoError(t, err)
	require.NotEmpty(t, txs)
	return txs[0].ID
}

func TestSetAnnotation_PersistsCommentAndColor(t *testing.T) {
	st := newTestStore(t)
	id := logTxForAnnotation(t, st)

	a := store.Annotation{HistoryID: id, Comment: "suspicious", Color: "red"}
	require.NoError(t, st.SetAnnotation(a))

	all, err := st.AllAnnotations()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, id, all[0].HistoryID)
	assert.Equal(t, "suspicious", all[0].Comment)
	assert.Equal(t, "red", all[0].Color)
}

func TestSetAnnotation_UpdatesExisting(t *testing.T) {
	st := newTestStore(t)
	id := logTxForAnnotation(t, st)

	require.NoError(t, st.SetAnnotation(store.Annotation{HistoryID: id, Comment: "first", Color: "blue"}))
	require.NoError(t, st.SetAnnotation(store.Annotation{HistoryID: id, Comment: "second", Color: "green"}))

	all, err := st.AllAnnotations()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "second", all[0].Comment)
	assert.Equal(t, "green", all[0].Color)
}

func TestDeleteAnnotation_RemovesRow(t *testing.T) {
	st := newTestStore(t)
	id := logTxForAnnotation(t, st)

	require.NoError(t, st.SetAnnotation(store.Annotation{HistoryID: id, Comment: "note", Color: "yellow"}))
	require.NoError(t, st.DeleteAnnotation(id))

	all, err := st.AllAnnotations()
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestDeleteAnnotation_NonExistent_NoError(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.DeleteAnnotation(9999))
}

func TestAllAnnotations_Empty(t *testing.T) {
	st := newTestStore(t)
	all, err := st.AllAnnotations()
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestAllAnnotations_MultipleRows(t *testing.T) {
	st := newTestStore(t)

	tx1 := makeTx("GET", "a.com", "https://a.com/", 200)
	tx2 := makeTx("POST", "b.com", "https://b.com/", 201)
	require.NoError(t, st.Log(tx1))
	require.NoError(t, st.Log(tx2))

	txs, err := st.AllTransactions()
	require.NoError(t, err)
	require.Len(t, txs, 2)

	require.NoError(t, st.SetAnnotation(store.Annotation{HistoryID: txs[0].ID, Comment: "c1", Color: "red"}))
	require.NoError(t, st.SetAnnotation(store.Annotation{HistoryID: txs[1].ID, Comment: "c2", Color: "blue"}))

	all, err := st.AllAnnotations()
	require.NoError(t, err)
	assert.Len(t, all, 2)
}
