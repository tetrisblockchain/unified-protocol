package main

import (
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"unified/core/consensus"
)

func TestFilterSeedEntriesSkipsHTTPFailures(t *testing.T) {
	t.Parallel()

	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okServer.Close()

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, okServer.URL, http.StatusMovedPermanently)
	}))
	defer redirectServer.Close()

	forbiddenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer forbiddenServer.Close()

	filtered, skipped, err := filterSeedEntries(log.Default(), []seedFileEntry{
		{URL: okServer.URL, Query: "ok"},
		{URL: redirectServer.URL, Query: "redirect"},
		{URL: forbiddenServer.URL, Query: "forbidden"},
	}, seedPreflightConfig{
		Enabled:   true,
		Timeout:   2 * time.Second,
		UserAgent: consensus.DefaultCrawlerUserAgent,
	})
	if err != nil {
		t.Fatalf("filterSeedEntries returned error: %v", err)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if len(filtered) != 2 {
		t.Fatalf("filtered len = %d, want 2", len(filtered))
	}
	if filtered[0].URL != okServer.URL {
		t.Fatalf("filtered[0].URL = %s, want %s", filtered[0].URL, okServer.URL)
	}
	if filtered[1].URL != redirectServer.URL {
		t.Fatalf("filtered[1].URL = %s, want %s", filtered[1].URL, redirectServer.URL)
	}
}

func TestFilterSeedEntriesDisabledLeavesListUntouched(t *testing.T) {
	t.Parallel()

	entries := []seedFileEntry{
		{URL: "https://example.com", Query: "one"},
		{URL: "https://openai.com", Query: "two"},
	}

	filtered, skipped, err := filterSeedEntries(nil, entries, seedPreflightConfig{Enabled: false})
	if err != nil {
		t.Fatalf("filterSeedEntries returned error: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if len(filtered) != len(entries) {
		t.Fatalf("filtered len = %d, want %d", len(filtered), len(entries))
	}
	for i := range entries {
		if filtered[i] != entries[i] {
			t.Fatalf("filtered[%d] = %#v, want %#v", i, filtered[i], entries[i])
		}
	}
}
