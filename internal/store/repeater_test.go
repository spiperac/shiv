package store_test

import (
	"testing"

	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTab(name, host string, port int, tls bool) store.RepeaterTab {
	return store.RepeaterTab{
		Name:       name,
		Host:       host,
		Port:       port,
		TLS:        tls,
		RawRequest: "GET / HTTP/1.1\r\nHost: " + host + "\r\n\r\n",
	}
}

func TestSaveRepeaterTab_PersistsAllFields(t *testing.T) {
	st := newTestStore(t)

	tab := store.RepeaterTab{
		Name:       "My Tab",
		Host:       "example.com",
		Port:       443,
		TLS:        true,
		RawRequest: "GET /api HTTP/1.1\r\nHost: example.com\r\n\r\n",
	}
	id, err := st.SaveRepeaterTab(tab)
	require.NoError(t, err)
	assert.Positive(t, id)

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	require.Len(t, tabs, 1)

	got := tabs[0]
	assert.Equal(t, id, got.ID)
	assert.Equal(t, "My Tab", got.Name)
	assert.Equal(t, "example.com", got.Host)
	assert.Equal(t, 443, got.Port)
	assert.True(t, got.TLS)
	assert.Equal(t, tab.RawRequest, got.RawRequest)
}

func TestSaveRepeaterTab_TLSFalse(t *testing.T) {
	st := newTestStore(t)

	id, err := st.SaveRepeaterTab(makeTab("HTTP Tab", "example.com", 80, false))
	require.NoError(t, err)
	assert.Positive(t, id)

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.False(t, tabs[0].TLS)
}

func TestAllRepeaterTabs_OrderedByPosition(t *testing.T) {
	st := newTestStore(t)

	for _, name := range []string{"First", "Second", "Third"} {
		_, err := st.SaveRepeaterTab(makeTab(name, "example.com", 80, false))
		require.NoError(t, err)
	}

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	require.Len(t, tabs, 3)
	assert.Equal(t, "First", tabs[0].Name)
	assert.Equal(t, "Second", tabs[1].Name)
	assert.Equal(t, "Third", tabs[2].Name)
}

func TestAllRepeaterTabs_PositionAutoIncrements(t *testing.T) {
	st := newTestStore(t)

	_, err := st.SaveRepeaterTab(makeTab("A", "a.com", 80, false))
	require.NoError(t, err)
	_, err = st.SaveRepeaterTab(makeTab("B", "b.com", 80, false))
	require.NoError(t, err)

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Less(t, tabs[0].Position, tabs[1].Position)
}

func TestUpdateRepeaterTab_UpdatesRequestAndResponse(t *testing.T) {
	st := newTestStore(t)

	id, err := st.SaveRepeaterTab(makeTab("Tab", "example.com", 80, false))
	require.NoError(t, err)

	newReq := "POST /submit HTTP/1.1\r\nHost: example.com\r\n\r\nbody"
	newResp := "HTTP/1.1 200 OK\r\n\r\nok"
	require.NoError(t, st.UpdateRepeaterTab(id, newReq, newResp))

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, newReq, tabs[0].RawRequest)
	assert.Equal(t, newResp, tabs[0].LastResponse)
}

func TestUpdateRepeaterTab_NonExistentIDNoError(t *testing.T) {
	st := newTestStore(t)
	// UPDATE on a non-existent row is a no-op in SQLite, not an error
	assert.NoError(t, st.UpdateRepeaterTab(9999, "req", "resp"))
}

func TestRenameRepeaterTab(t *testing.T) {
	st := newTestStore(t)

	id, err := st.SaveRepeaterTab(makeTab("Old", "example.com", 80, false))
	require.NoError(t, err)

	require.NoError(t, st.RenameRepeaterTab(id, "New Name"))

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, "New Name", tabs[0].Name)
}

func TestDeleteRepeaterTab(t *testing.T) {
	st := newTestStore(t)

	id, err := st.SaveRepeaterTab(makeTab("Tab", "example.com", 80, false))
	require.NoError(t, err)

	require.NoError(t, st.DeleteRepeaterTab(id))

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	assert.Empty(t, tabs)
}

func TestDeleteRepeaterTab_NonExistent(t *testing.T) {
	st := newTestStore(t)
	assert.NoError(t, st.DeleteRepeaterTab(9999))
}

func TestDeleteRepeaterTab_DoesNotAffectOtherTabs(t *testing.T) {
	st := newTestStore(t)

	idKeep, err := st.SaveRepeaterTab(makeTab("Keep", "keep.com", 80, false))
	require.NoError(t, err)
	idDel, err := st.SaveRepeaterTab(makeTab("Delete", "del.com", 80, false))
	require.NoError(t, err)

	require.NoError(t, st.DeleteRepeaterTab(idDel))

	tabs, err := st.AllRepeaterTabs()
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, idKeep, tabs[0].ID)
	assert.Equal(t, "Keep", tabs[0].Name)
}
