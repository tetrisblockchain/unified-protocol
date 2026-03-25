package core

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"strings"
	"sync"
	"time"
)

type MiningStatus struct {
	MinerAddress             string    `json:"minerAddress"`
	PartitionEnabled         bool      `json:"partitionEnabled"`
	PartitionLocalWorker     string    `json:"partitionLocalWorker,omitempty"`
	PartitionWorkerCount     int       `json:"partitionWorkerCount"`
	PartitionWorkers         []string  `json:"partitionWorkers,omitempty"`
	LastEvaluatedSearchTasks int       `json:"lastEvaluatedSearchTasks"`
	LastOwnedSearchTasks     int       `json:"lastOwnedSearchTasks"`
	LastSkippedSearchTasks   int       `json:"lastSkippedSearchTasks"`
	LastOwnedMempoolTasks    int       `json:"lastOwnedMempoolTasks"`
	LastOwnedFrontierTasks   int       `json:"lastOwnedFrontierTasks"`
	UpdatedAt                time.Time `json:"updatedAt"`
}

type PeerTaskPartitioner struct {
	localWorkerID string
	workers       func() []string

	mu   sync.RWMutex
	last MiningStatus
}

func NewPeerTaskPartitioner(localWorkerID string, workers func() []string) *PeerTaskPartitioner {
	if strings.TrimSpace(localWorkerID) == "" || workers == nil {
		return nil
	}
	return &PeerTaskPartitioner{
		localWorkerID: strings.TrimSpace(localWorkerID),
		workers:       workers,
	}
}

func (p *PeerTaskPartitioner) FilterWorkItems(workItems []miningSearchTask) ([]miningSearchTask, []miningSearchTask, MiningStatus) {
	status := MiningStatus{
		PartitionEnabled:         p != nil,
		LastEvaluatedSearchTasks: len(workItems),
		LastOwnedSearchTasks:     len(workItems),
		UpdatedAt:                time.Now().UTC(),
	}
	if p == nil {
		return append([]miningSearchTask(nil), workItems...), nil, status
	}

	workers := p.workerIDs()
	status.PartitionLocalWorker = p.localWorkerID
	status.PartitionWorkerCount = len(workers)
	status.PartitionWorkers = append([]string(nil), workers...)
	if len(workers) == 0 {
		status.PartitionEnabled = false
		return append([]miningSearchTask(nil), workItems...), nil, status
	}

	owned := make([]miningSearchTask, 0, len(workItems))
	skipped := make([]miningSearchTask, 0, len(workItems))
	status.LastOwnedSearchTasks = 0
	for _, item := range workItems {
		if partitionOwnerForTask(workers, item) != p.localWorkerID {
			skipped = append(skipped, item)
			status.LastSkippedSearchTasks++
			continue
		}
		owned = append(owned, item)
		status.LastOwnedSearchTasks++
		switch item.source {
		case miningTaskSourceMempool:
			status.LastOwnedMempoolTasks++
		case miningTaskSourceFrontier:
			status.LastOwnedFrontierTasks++
		}
	}

	p.mu.Lock()
	p.last = status
	p.mu.Unlock()
	return owned, skipped, status
}

func (p *PeerTaskPartitioner) Snapshot() MiningStatus {
	if p == nil {
		return MiningStatus{}
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	snapshot := p.last
	snapshot.PartitionWorkers = append([]string(nil), p.last.PartitionWorkers...)
	return snapshot
}

func (p *PeerTaskPartitioner) workerIDs() []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 8)
	appendWorker := func(value string) {
		cleaned := strings.TrimSpace(value)
		if cleaned == "" {
			return
		}
		if _, ok := seen[cleaned]; ok {
			return
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}

	appendWorker(p.localWorkerID)
	for _, worker := range p.workers() {
		appendWorker(worker)
	}
	sort.Strings(out)
	return out
}

func partitionOwnerForTask(workers []string, item miningSearchTask) string {
	if len(workers) == 0 {
		return ""
	}
	key := partitionTaskKey(item)
	sum := sha256.Sum256([]byte(key))
	slot := binary.BigEndian.Uint64(sum[:8]) % uint64(len(workers))
	return workers[slot]
}

func partitionTaskKey(item miningSearchTask) string {
	if item.source == miningTaskSourceMempool {
		return firstNonEmptyString(item.envelope.Transaction.Hash, item.record.TxHash, item.record.ID, item.record.URL)
	}
	return firstNonEmptyString(item.record.ID, item.record.TxHash, item.record.URL)
}
