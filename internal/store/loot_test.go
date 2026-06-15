package store_test

import (
	"testing"
	"time"

	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeLoot(title, severity, notes string) store.LootEntry {
	return store.LootEntry{
		Title:    title,
		Severity: severity,
		Notes:    notes,
	}
}

func TestAddLoot_PersistsAllFields(t *testing.T) {
	st := newTestStore(t)

	entry := store.LootEntry{
		Title:    "Admin panel exposed",
		Severity: "High",
		Notes:    "Found at /admin with no auth",
	}
	id, err := st.AddLoot(entry)
	require.NoError(t, err)
	assert.Positive(t, id)

	entries, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, entries, 1)

	got := entries[0]
	assert.Equal(t, id, got.ID)
	assert.Equal(t, "Admin panel exposed", got.Title)
	assert.Equal(t, "High", got.Severity)
	assert.Equal(t, "Found at /admin with no auth", got.Notes)
	assert.Nil(t, got.HistoryID)
	assert.WithinDuration(t, time.Now(), got.CreatedAt, 5*time.Second)
}

func TestAddLoot_WithLinkedHistoryID(t *testing.T) {
	st := newTestStore(t)

	// first log a transaction to get a valid history ID
	tx := makeTx("GET", "example.com", "https://example.com/secret", 200)
	require.NoError(t, st.Log(tx))
	txs, err := st.AllTransactions()
	require.NoError(t, err)
	require.Len(t, txs, 1)
	histID := txs[0].ID

	entry := store.LootEntry{
		Title:     "Secret endpoint",
		Severity:  "Critical",
		Notes:     "Returns sensitive data",
		HistoryID: &histID,
	}
	id, err := st.AddLoot(entry)
	require.NoError(t, err)
	assert.Positive(t, id)

	entries, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].HistoryID)
	assert.Equal(t, histID, *entries[0].HistoryID)
}

func TestAllLoot_OrderedBySeverityThenIDDesc(t *testing.T) {
	st := newTestStore(t)

	// Insert in reverse severity order to confirm sorting
	_, err := st.AddLoot(makeLoot("Info finding", "Info", ""))
	require.NoError(t, err)
	_, err = st.AddLoot(makeLoot("Low finding", "Low", ""))
	require.NoError(t, err)
	_, err = st.AddLoot(makeLoot("Critical finding", "Critical", ""))
	require.NoError(t, err)
	_, err = st.AddLoot(makeLoot("High finding", "High", ""))
	require.NoError(t, err)
	_, err = st.AddLoot(makeLoot("Medium finding", "Medium", ""))
	require.NoError(t, err)

	entries, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, entries, 5)

	assert.Equal(t, "Critical", entries[0].Severity)
	assert.Equal(t, "High", entries[1].Severity)
	assert.Equal(t, "Medium", entries[2].Severity)
	assert.Equal(t, "Low", entries[3].Severity)
	assert.Equal(t, "Info", entries[4].Severity)
}

func TestAllLoot_MultipleSameSeverityOrderedByIDDesc(t *testing.T) {
	st := newTestStore(t)

	id1, err := st.AddLoot(makeLoot("First High", "High", ""))
	require.NoError(t, err)
	id2, err := st.AddLoot(makeLoot("Second High", "High", ""))
	require.NoError(t, err)

	entries, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// same severity — ordered by id DESC so id2 comes first
	assert.Equal(t, id2, entries[0].ID)
	assert.Equal(t, id1, entries[1].ID)
}

func TestDeleteLoot(t *testing.T) {
	st := newTestStore(t)

	id, err := st.AddLoot(makeLoot("To delete", "Low", ""))
	require.NoError(t, err)

	require.NoError(t, st.DeleteLoot(id))

	entries, err := st.AllLoot()
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestDeleteLoot_NonExistent(t *testing.T) {
	st := newTestStore(t)
	assert.NoError(t, st.DeleteLoot(9999))
}

func TestDeleteLoot_DoesNotAffectOtherEntries(t *testing.T) {
	st := newTestStore(t)

	idKeep, err := st.AddLoot(makeLoot("Keep", "High", ""))
	require.NoError(t, err)
	idDel, err := st.AddLoot(makeLoot("Delete", "Low", ""))
	require.NoError(t, err)

	require.NoError(t, st.DeleteLoot(idDel))

	entries, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, idKeep, entries[0].ID)
}

func TestAllLoot_Empty(t *testing.T) {
	st := newTestStore(t)
	entries, err := st.AllLoot()
	require.NoError(t, err)
	assert.Empty(t, entries)
}
