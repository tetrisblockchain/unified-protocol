package core

import "testing"

func TestPeerTaskPartitionerAssignsSingleDeterministicOwner(t *testing.T) {
	t.Parallel()

	workers := []string{"peer-a", "peer-b", "peer-c"}
	items := []miningSearchTask{
		{source: miningTaskSourceMempool, record: SearchTaskRecord{ID: "task-1", TxHash: "tx-1", URL: "https://example.com/1"}},
		{source: miningTaskSourceMempool, record: SearchTaskRecord{ID: "task-2", TxHash: "tx-2", URL: "https://example.com/2"}},
		{source: miningTaskSourceFrontier, record: SearchTaskRecord{ID: "frontier-1", URL: "https://example.com/3"}},
	}

	for _, item := range items {
		owners := make(map[string]struct{})
		for _, worker := range workers {
			partitioner := NewPeerTaskPartitioner(worker, func() []string {
				return workers
			})
			filtered, skipped, status := partitioner.FilterWorkItems([]miningSearchTask{item})
			if status.PartitionWorkerCount != len(workers) {
				t.Fatalf("PartitionWorkerCount = %d, want %d", status.PartitionWorkerCount, len(workers))
			}
			if len(filtered) == 1 {
				owners[worker] = struct{}{}
				if len(skipped) != 0 {
					t.Fatalf("skipped length = %d, want 0", len(skipped))
				}
			} else if len(skipped) != 1 {
				t.Fatalf("skipped length = %d, want 1", len(skipped))
			}
		}
		if len(owners) != 1 {
			t.Fatalf("owners for %s = %d, want 1", partitionTaskKey(item), len(owners))
		}
	}
}
