package store_test

import (
	"os"
	"testing"

	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	f, err := os.CreateTemp("", "shiv-test-*.shiv")
	require.NoError(t, err)
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	st, err := store.Open(f.Name())
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSaveAndLoadRepeaterTab(t *testing.T) {
	st := newTestStore(t)

	tab := store.RepeaterTab{
		Name:       "Test Tab",
		Host:       "example.com",
		Port:       443,
		TLS:        true,
		RawRequest: "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n",
	}
	id, err := st.SaveRepeaterTab(tab)
	require.NoError(t, err)
	assert.Positive(t, id)

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	require.Len(t, tabs, 1)

	got := tabs[0]
	assert.Equal(t, id, got.ID)
	assert.Equal(t, "Test Tab", got.Name)
	assert.Equal(t, "example.com", got.Host)
	assert.Equal(t, 443, got.Port)
	assert.True(t, got.TLS)
	assert.Equal(t, tab.RawRequest, got.RawRequest)
}

func TestDeleteRepeaterTab(t *testing.T) {
	st := newTestStore(t)

	id, err := st.SaveRepeaterTab(store.RepeaterTab{Name: "Tab", Host: "example.com", Port: 80})
	require.NoError(t, err)

	require.NoError(t, st.DeleteRepeaterTab(id))

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	assert.Empty(t, tabs)
}

func TestDeleteRepeaterTab_NonExistent(t *testing.T) {
	st := newTestStore(t)
	// deleting a non-existent ID should not error
	assert.NoError(t, st.DeleteRepeaterTab(9999))
}

func TestUpdateRepeaterTab(t *testing.T) {
	st := newTestStore(t)

	id, err := st.SaveRepeaterTab(store.RepeaterTab{Name: "Tab", Host: "example.com", Port: 80})
	require.NoError(t, err)

	newReq := "POST /api HTTP/1.1\r\nHost: example.com\r\n\r\n{}"
	newResp := "HTTP/1.1 200 OK\r\n\r\n"
	require.NoError(t, st.UpdateRepeaterTab(id, newReq, newResp))

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, newReq, tabs[0].RawRequest)
	assert.Equal(t, newResp, tabs[0].LastResponse)
}

func TestAllRepeaterTabs_OrderByPosition(t *testing.T) {
	st := newTestStore(t)

	for _, name := range []string{"First", "Second", "Third"} {
		_, err := st.SaveRepeaterTab(store.RepeaterTab{Name: name, Host: "example.com", Port: 80})
		require.NoError(t, err)
	}

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	require.Len(t, tabs, 3)
	assert.Equal(t, "First", tabs[0].Name)
	assert.Equal(t, "Second", tabs[1].Name)
	assert.Equal(t, "Third", tabs[2].Name)
}

func TestSaveRepeaterTab_MultipleTabs(t *testing.T) {
	st := newTestStore(t)

	id1, err := st.SaveRepeaterTab(store.RepeaterTab{Name: "A", Host: "a.com", Port: 80})
	require.NoError(t, err)
	id2, err := st.SaveRepeaterTab(store.RepeaterTab{Name: "B", Host: "b.com", Port: 443, TLS: true})
	require.NoError(t, err)

	assert.NotEqual(t, id1, id2)

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	assert.Len(t, tabs, 2)
}

func TestRenameRepeaterTab(t *testing.T) {
	st := newTestStore(t)

	id, err := st.SaveRepeaterTab(store.RepeaterTab{Name: "Old Name", Host: "example.com", Port: 80})
	require.NoError(t, err)

	require.NoError(t, st.RenameRepeaterTab(id, "New Name"))

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, "New Name", tabs[0].Name)
}

func TestDeleteTab_DoesNotAffectOthers(t *testing.T) {
	st := newTestStore(t)

	id1, _ := st.SaveRepeaterTab(store.RepeaterTab{Name: "Keep", Host: "keep.com", Port: 80})
	id2, _ := st.SaveRepeaterTab(store.RepeaterTab{Name: "Delete", Host: "del.com", Port: 80})

	require.NoError(t, st.DeleteRepeaterTab(id2))

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, id1, tabs[0].ID)
	assert.Equal(t, "Keep", tabs[0].Name)
}
