package consensus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"testing"
	"time"

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

func TestNewCrawlerTransportEnablesConnectionReuse(t *testing.T) {
	t.Parallel()

	transport := NewCrawlerTransport(5*time.Second, 0, 0, 0, 0)
	if transport.DisableKeepAlives {
		t.Fatal("DisableKeepAlives = true, want false")
	}
	if transport.ResponseHeaderTimeout != 5*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %s, want %s", transport.ResponseHeaderTimeout, 5*time.Second)
	}
	if transport.MaxIdleConns != DefaultCrawlerMaxIdleConns {
		t.Fatalf("MaxIdleConns = %d, want %d", transport.MaxIdleConns, DefaultCrawlerMaxIdleConns)
	}
	if transport.MaxIdleConnsPerHost != DefaultCrawlerMaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, want %d", transport.MaxIdleConnsPerHost, DefaultCrawlerMaxIdleConnsPerHost)
	}
	if transport.MaxConnsPerHost != DefaultCrawlerMaxConnsPerHost {
		t.Fatalf("MaxConnsPerHost = %d, want %d", transport.MaxConnsPerHost, DefaultCrawlerMaxConnsPerHost)
	}
}

func TestCrawlLimiterSerializesSameHost(t *testing.T) {
	t.Parallel()

	limiter := NewCrawlLimiter(0, 1)
	ctx := context.Background()
	releaseFirst, err := limiter.Acquire(ctx, "https://docs.example.com/guide")
	if err != nil {
		t.Fatalf("Acquire first returned error: %v", err)
	}
	defer releaseFirst()

	acquiredSecond := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		releaseSecond, err := limiter.Acquire(ctx, "https://docs.example.com/api")
		if err != nil {
			errCh <- err
			return
		}
		releaseSecond()
		close(acquiredSecond)
	}()

	select {
	case err := <-errCh:
		t.Fatalf("Acquire second returned error: %v", err)
	case <-acquiredSecond:
		t.Fatal("second acquire succeeded before first release")
	case <-time.After(100 * time.Millisecond):
	}

	releaseFirst()

	select {
	case err := <-errCh:
		t.Fatalf("Acquire second returned error: %v", err)
	case <-acquiredSecond:
	case <-time.After(time.Second):
		t.Fatal("second acquire did not complete after release")
	}
}
