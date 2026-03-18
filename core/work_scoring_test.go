package core

import (
	"testing"

	"unified/core/consensus"
)

func TestComputeBlockWorkDampensRepeatedHosts(t *testing.T) {
	t.Parallel()

	state := NewStateSnapshot()
	state.PendingTasks["same-a"] = SearchTaskRecord{ID: "same-a", Submitter: "alice", Difficulty: 10}
	state.PendingTasks["same-b"] = SearchTaskRecord{ID: "same-b", Submitter: "alice", Difficulty: 10}
	state.PendingTasks["diverse-a"] = SearchTaskRecord{ID: "diverse-a", Submitter: "alice", Difficulty: 10}
	state.PendingTasks["diverse-b"] = SearchTaskRecord{ID: "diverse-b", Submitter: "alice", Difficulty: 10}

	page := consensus.IndexedPage{
		Title:       "Example",
		Snippet:     "Search index",
		Body:        "Distributed search on campus knowledge graphs and archival collections",
		ContentHash: "abc123",
		SimHash:     42,
	}

	sameHostWork, err := computeBlockWork(state, nil, []CrawlProof{
		{TaskID: "same-a", URL: "https://example.edu/a", Page: page},
		{TaskID: "same-b", URL: "https://example.edu/b", Page: page},
	})
	if err != nil {
		t.Fatalf("computeBlockWork same host: %v", err)
	}
	diverseHostWork, err := computeBlockWork(state, nil, []CrawlProof{
		{TaskID: "diverse-a", URL: "https://example.edu/a", Page: page},
		{TaskID: "diverse-b", URL: "https://library.example.org/b", Page: page},
	})
	if err != nil {
		t.Fatalf("computeBlockWork diverse host: %v", err)
	}

	if diverseHostWork.Cmp(sameHostWork) <= 0 {
		t.Fatalf("diverse host work = %s, want > same-host work %s", diverseHostWork.String(), sameHostWork.String())
	}
}
