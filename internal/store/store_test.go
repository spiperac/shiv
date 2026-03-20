package store_test

import (
	"os"
	"testing"

	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	f, err := os.CreateTemp("", "shiv-store-test-*.shiv")
	require.NoError(t, err)
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	st, err := store.Open(f.Name())
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return st
}
