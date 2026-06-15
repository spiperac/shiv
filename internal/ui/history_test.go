package ui

import (
	"testing"

	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
)

func TestTreePathOf(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"absolute https no query", "https://example.com/api/v1", "/api/v1"},
		{"absolute https with query", "https://example.com/api/v1?page=1&limit=10", "/api/v1"},
		{"absolute http with query", "http://example.com/search?q=foo&lang=en", "/search"},
		{"root with query", "https://example.com/?utm_source=x", "/"},
		{"no path", "https://example.com", "/"},
		{"path only no query", "/api/users", "/api/users"},
		{"path only with query", "/api/users?id=123", "/api/users"},
		{"root path with query", "/?q=foo", "/"},
		{"empty string", "", ""},
		{"query only after root", "https://example.com?x=1", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, treePathOf(tt.url))
		})
	}
}

func TestSiteMap_Add_DeduplicatesQueryStrings(t *testing.T) {
	sm := newSiteMap()
	sm.add(store.Transaction{Host: "example.com", Port: 443, URL: "https://example.com/api?page=1"})
	sm.add(store.Transaction{Host: "example.com", Port: 443, URL: "https://example.com/api?page=2"})
	sm.add(store.Transaction{Host: "example.com", Port: 443, URL: "https://example.com/api?page=3"})

	hostUIDs := sm.childUIDs("")
	assert.Len(t, hostUIDs, 1, "should have exactly one host")

	pathUIDs := sm.childUIDs(hostUIDs[0])
	assert.Len(t, pathUIDs, 1, "query strings must be deduplicated to a single /api node")
}

func TestSiteMap_Add_SeparatesByPort(t *testing.T) {
	sm := newSiteMap()
	sm.add(store.Transaction{Host: "example.com", Port: 80, URL: "http://example.com/"})
	sm.add(store.Transaction{Host: "example.com", Port: 8080, URL: "http://example.com:8080/"})
	sm.add(store.Transaction{Host: "example.com", Port: 443, URL: "https://example.com/"})

	assert.Len(t, sm.childUIDs(""), 3, "each port must be a separate tree entry")
}

func TestSiteMap_TextFilter(t *testing.T) {
	sm := newSiteMap()
	sm.add(store.Transaction{Host: "example.com", Port: 80, URL: "http://example.com/"})
	sm.add(store.Transaction{Host: "other.com", Port: 80, URL: "http://other.com/"})
	sm.add(store.Transaction{Host: "test.example.com", Port: 80, URL: "http://test.example.com/"})

	assert.Len(t, sm.childUIDs(""), 3, "no filter: all hosts visible")

	sm.mu.Lock()
	sm.textFilter = "example"
	sm.mu.Unlock()
	assert.Len(t, sm.childUIDs(""), 2, "filter 'example': matches example.com and test.example.com")

	sm.mu.Lock()
	sm.textFilter = "other"
	sm.mu.Unlock()
	assert.Len(t, sm.childUIDs(""), 1, "filter 'other': matches only other.com")

	sm.mu.Lock()
	sm.textFilter = "EXAMPLE"
	sm.mu.Unlock()
	assert.Len(t, sm.childUIDs(""), 2, "filter must be case-insensitive")

	sm.mu.Lock()
	sm.textFilter = "xyz"
	sm.mu.Unlock()
	assert.Len(t, sm.childUIDs(""), 0, "filter 'xyz': no matches")

	sm.mu.Lock()
	sm.textFilter = ""
	sm.mu.Unlock()
	assert.Len(t, sm.childUIDs(""), 3, "cleared filter: all hosts visible again")
}

func TestSiteMap_TextFilter_NonStandardPort(t *testing.T) {
	sm := newSiteMap()
	sm.add(store.Transaction{Host: "portquiz.net", Port: 8080, URL: "http://portquiz.net:8080/"})
	sm.add(store.Transaction{Host: "portquiz.net", Port: 80, URL: "http://portquiz.net/"})

	// Port 8080 displays as "portquiz.net:8080" — filter on port should work
	sm.mu.Lock()
	sm.textFilter = "8080"
	sm.mu.Unlock()
	assert.Len(t, sm.childUIDs(""), 1, "filter on port number must match display host")

	// Filter on hostname alone must match both ports
	sm.mu.Lock()
	sm.textFilter = "portquiz"
	sm.mu.Unlock()
	assert.Len(t, sm.childUIDs(""), 2, "filter on hostname must match all ports")
}

func TestSiteMap_Add_DeepPaths(t *testing.T) {
	sm := newSiteMap()
	sm.add(store.Transaction{Host: "example.com", Port: 443, URL: "https://example.com/api/v1/users"})
	sm.add(store.Transaction{Host: "example.com", Port: 443, URL: "https://example.com/api/v1/posts"})
	sm.add(store.Transaction{Host: "example.com", Port: 443, URL: "https://example.com/api/v2/users"})

	hostUIDs := sm.childUIDs("")
	assert.Len(t, hostUIDs, 1)

	// /api
	apiUIDs := sm.childUIDs(hostUIDs[0])
	assert.Len(t, apiUIDs, 1, "should have one /api child")

	// /api/v1 and /api/v2
	versionUIDs := sm.childUIDs(apiUIDs[0])
	assert.Len(t, versionUIDs, 2, "should have v1 and v2 children")
}
