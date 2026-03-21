package core

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestPeerReputationBookBansAndRecovers(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	book := NewPeerReputationBook()
	book.now = func() time.Time { return now }

	id := peer.ID("peer-a")
	status := book.Penalize(id, 85, "invalid block")
	if !status.Banned {
		t.Fatalf("status.Banned = false, want true")
	}
	if got := book.AdaptiveLimit(id, 40); got != 0 {
		t.Fatalf("AdaptiveLimit while banned = %d, want 0", got)
	}
	if book.Allowed(id, now) {
		t.Fatalf("Allowed during ban = true, want false")
	}

	now = now.Add(DefaultPeerBanDuration + 2*DefaultPeerRecoveryWindow)
	if !book.Allowed(id, now) {
		t.Fatalf("Allowed after recovery = false, want true")
	}
	if got := book.AdaptiveLimit(id, 40); got != 20 {
		t.Fatalf("AdaptiveLimit after recovery = %d, want 20", got)
	}

	status = book.Reward(id, 80, "valid sync response")
	if status.Score != MaxPeerScore {
		t.Fatalf("score after reward = %d, want %d", status.Score, MaxPeerScore)
	}
	if got := book.AdaptiveLimit(id, 40); got != 40 {
		t.Fatalf("AdaptiveLimit after reward = %d, want 40", got)
	}
}
