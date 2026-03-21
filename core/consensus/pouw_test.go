package consensus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"testing"

	coregov "unified/core/governance"
)

func TestPriorityRegistryApplySuffixRule(t *testing.T) {
	t.Parallel()

	registry := NewPriorityRegistry()
	if err := registry.UpsertRule(coregov.PriorityRule{
		Sector:        ".edu",
		MultiplierBPS: 15000,
	}); err != nil {
		t.Fatalf("UpsertRule returned error: %v", err)
	}

	task, err := NewCrawlTask("distributed search", []string{"https://mit.edu"}, big.NewInt(100), 20, 10, 0)
	if err != nil {
		t.Fatalf("NewCrawlTask returned error: %v", err)
	}

	adjustment, err := registry.Apply(task, "https://mit.edu")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	if adjustment.MultiplierBPS != 15000 {
		t.Fatalf("MultiplierBPS = %d, want 15000", adjustment.MultiplierBPS)
	}
	if adjustment.AdjustedDifficulty != 30 {
		t.Fatalf("AdjustedDifficulty = %d, want 30", adjustment.AdjustedDifficulty)
	}
	if adjustment.AdjustedBounty.String() != "450" {
		t.Fatalf("AdjustedBounty = %s, want 450", adjustment.AdjustedBounty.String())
	}
	if adjustment.ArchitectFee.String() != "14" {
		t.Fatalf("ArchitectFee = %s, want 14", adjustment.ArchitectFee.String())
	}
	if adjustment.NetMinerReward.String() != "436" {
		t.Fatalf("NetMinerReward = %s, want 436", adjustment.NetMinerReward.String())
	}
}

func TestBuildProofHashMatchesCanonicalPayload(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(struct {
		TaskID      string `json:"taskId"`
		URL         string `json:"url"`
		ContentHash string `json:"contentHash"`
		SimHash     uint64 `json:"simHash"`
	}{
		TaskID:      "task-1",
		URL:         "https://example.com",
		ContentHash: "content-hash",
		SimHash:     42,
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	sum := sha256.Sum256(payload)
	want := hex.EncodeToString(sum[:])

	if got := buildProofHash("task-1", "https://example.com", "content-hash", 42); got != want {
		t.Fatalf("buildProofHash = %s, want %s", got, want)
	}
}
