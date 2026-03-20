package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddScopeEntry_AndRetrieve(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.AddScopeEntry("example.com"))

	entries, err := st.AllScopeEntries()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "example.com", entries[0].Host)
	assert.Positive(t, entries[0].ID)
}

func TestAddScopeEntry_Multiple(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.AddScopeEntry("example.com"))
	require.NoError(t, st.AddScopeEntry("other.com"))

	entries, err := st.AllScopeEntries()
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestAddScopeEntry_Duplicate_Errors(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.AddScopeEntry("example.com"))
	err := st.AddScopeEntry("example.com")
	assert.Error(t, err, "duplicate host must be rejected by UNIQUE constraint")
}

func TestDeleteScopeEntry(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.AddScopeEntry("example.com"))
	entries, err := st.AllScopeEntries()
	require.NoError(t, err)
	require.Len(t, entries, 1)

	require.NoError(t, st.DeleteScopeEntry(entries[0].ID))

	entries, err = st.AllScopeEntries()
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestDeleteScopeEntry_NonExistent(t *testing.T) {
	st := newTestStore(t)
	assert.NoError(t, st.DeleteScopeEntry(9999))
}

func TestAllScopeEntries_OrderedByIDAsc(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.AddScopeEntry("alpha.com"))
	require.NoError(t, st.AddScopeEntry("beta.com"))
	require.NoError(t, st.AddScopeEntry("gamma.com"))

	entries, err := st.AllScopeEntries()
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, "alpha.com", entries[0].Host)
	assert.Equal(t, "beta.com", entries[1].Host)
	assert.Equal(t, "gamma.com", entries[2].Host)
}

func TestInScope_EmptyScopeAllowsEverything(t *testing.T) {
	st := newTestStore(t)
	assert.True(t, st.InScope("anything.com"))
	assert.True(t, st.InScope("other.net"))
}

func TestInScope_ExactMatch(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.AddScopeEntry("example.com"))

	assert.True(t, st.InScope("example.com"))
}

func TestInScope_SubdomainMatch(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.AddScopeEntry("example.com"))

	assert.True(t, st.InScope("sub.example.com"))
	assert.True(t, st.InScope("deep.sub.example.com"))
}

func TestInScope_NoMatch(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.AddScopeEntry("example.com"))

	assert.False(t, st.InScope("other.com"))
	assert.False(t, st.InScope("notexample.com"))
	assert.False(t, st.InScope("example.com.evil.com"))
}

func TestInScope_StripsPort(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.AddScopeEntry("example.com"))

	assert.True(t, st.InScope("example.com:8080"))
	assert.True(t, st.InScope("example.com:443"))
}

func TestInScope_AfterDeleteScopeBecomesOpen(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.AddScopeEntry("example.com"))

	entries, err := st.AllScopeEntries()
	require.NoError(t, err)
	require.NoError(t, st.DeleteScopeEntry(entries[0].ID))

	// scope is now empty — everything is in scope
	assert.True(t, st.InScope("example.com"))
	assert.True(t, st.InScope("completely-unrelated.com"))
}

func TestInScope_MultipleEntries(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.AddScopeEntry("example.com"))
	require.NoError(t, st.AddScopeEntry("target.io"))

	assert.True(t, st.InScope("example.com"))
	assert.True(t, st.InScope("api.target.io"))
	assert.False(t, st.InScope("other.com"))
}
