package core

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestPeerWindowLimiterEnforcesWindow(t *testing.T) {
	t.Parallel()

	limiter := newPeerWindowLimiter(1, time.Minute)
	id := peer.ID("peer-1")
	now := time.Unix(1_000, 0)

	if !limiter.Allow(id, now) {
		t.Fatalf("first Allow returned false, want true")
	}
	if limiter.Allow(id, now.Add(10*time.Second)) {
		t.Fatalf("second Allow within window returned true, want false")
	}
	if !limiter.Allow(id, now.Add(time.Minute+time.Second)) {
		t.Fatalf("Allow after window reset returned false, want true")
	}
}
