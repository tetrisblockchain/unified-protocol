package core

import (
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	DefaultPeerScore          = 100
	MinPeerScore              = 0
	MaxPeerScore              = 100
	LowPeerScoreThreshold     = 50
	BannedPeerScoreThreshold  = 20
	DefaultPeerBanDuration    = 15 * time.Minute
	DefaultPeerRecoveryWindow = 5 * time.Minute
	DefaultPeerRecoveryStep   = 5
)

type PeerReputationStatus struct {
	PeerID      string    `json:"peerId"`
	Score       int       `json:"score"`
	Connected   bool      `json:"connected"`
	Banned      bool      `json:"banned"`
	BannedUntil time.Time `json:"bannedUntil,omitempty"`
	Successes   uint64    `json:"successes"`
	Penalties   uint64    `json:"penalties"`
	LastReason  string    `json:"lastReason,omitempty"`
	LastUpdated time.Time `json:"lastUpdated,omitempty"`
}

type PeerReputationSummary struct {
	ConnectedPeers     int `json:"connectedPeers"`
	TrackedPeers       int `json:"trackedPeers"`
	LowReputationPeers int `json:"lowReputationPeers"`
	BannedPeers        int `json:"bannedPeers"`
}

type peerReputationEntry struct {
	score       int
	bannedUntil time.Time
	successes   uint64
	penalties   uint64
	lastReason  string
	lastUpdated time.Time
}

type PeerReputationBook struct {
	mu             sync.Mutex
	entries        map[peer.ID]*peerReputationEntry
	banDuration    time.Duration
	recoveryWindow time.Duration
	recoveryStep   int
	now            func() time.Time
}

func NewPeerReputationBook() *PeerReputationBook {
	return &PeerReputationBook{
		entries:        make(map[peer.ID]*peerReputationEntry),
		banDuration:    DefaultPeerBanDuration,
		recoveryWindow: DefaultPeerRecoveryWindow,
		recoveryStep:   DefaultPeerRecoveryStep,
		now:            time.Now,
	}
}

func (b *PeerReputationBook) Reward(id peer.ID, delta int, reason string) PeerReputationStatus {
	if delta <= 0 {
		delta = 1
	}
	now := b.currentTime()

	b.mu.Lock()
	defer b.mu.Unlock()

	entry := b.entryLocked(id)
	b.recoverLocked(entry, now)
	entry.score += delta
	if entry.score > MaxPeerScore {
		entry.score = MaxPeerScore
	}
	entry.successes++
	entry.lastReason = reason
	entry.lastUpdated = now
	return snapshotPeerStatus(id, entry, false, now)
}

func (b *PeerReputationBook) Penalize(id peer.ID, delta int, reason string) PeerReputationStatus {
	if delta <= 0 {
		delta = 1
	}
	now := b.currentTime()

	b.mu.Lock()
	defer b.mu.Unlock()

	entry := b.entryLocked(id)
	b.recoverLocked(entry, now)
	entry.score -= delta
	if entry.score < MinPeerScore {
		entry.score = MinPeerScore
	}
	entry.penalties++
	entry.lastReason = reason
	entry.lastUpdated = now
	if entry.score <= BannedPeerScoreThreshold {
		entry.bannedUntil = now.Add(b.banDuration)
	}
	return snapshotPeerStatus(id, entry, false, now)
}

func (b *PeerReputationBook) Allowed(id peer.ID, now time.Time) bool {
	if b == nil || id == "" {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	entry := b.entryLocked(id)
	b.recoverLocked(entry, now)
	return !entry.isBanned(now)
}

func (b *PeerReputationBook) AdaptiveLimit(id peer.ID, base int) int {
	if b == nil || base <= 0 || id == "" {
		return base
	}
	now := b.currentTime()

	b.mu.Lock()
	defer b.mu.Unlock()

	entry := b.entryLocked(id)
	b.recoverLocked(entry, now)
	if entry.isBanned(now) {
		return 0
	}

	switch {
	case entry.score >= 80:
		return base
	case entry.score >= 60:
		return scaledLimit(base, 75)
	case entry.score >= 40:
		return scaledLimit(base, 50)
	default:
		return scaledLimit(base, 25)
	}
}

func (b *PeerReputationBook) Snapshot(connected map[peer.ID]bool) []PeerReputationStatus {
	if b == nil {
		return nil
	}
	now := b.currentTime()

	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]PeerReputationStatus, 0, len(b.entries))
	for id, entry := range b.entries {
		b.recoverLocked(entry, now)
		out = append(out, snapshotPeerStatus(id, entry, connected[id], now))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].PeerID < out[j].PeerID
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func (b *PeerReputationBook) Summary(connected map[peer.ID]bool) PeerReputationSummary {
	snapshot := b.Snapshot(connected)
	summary := PeerReputationSummary{
		TrackedPeers: len(snapshot),
	}
	for _, status := range snapshot {
		if status.Connected {
			summary.ConnectedPeers++
		}
		if status.Score < LowPeerScoreThreshold {
			summary.LowReputationPeers++
		}
		if status.Banned {
			summary.BannedPeers++
		}
	}
	return summary
}

func (b *PeerReputationBook) Score(id peer.ID) int {
	if b == nil || id == "" {
		return DefaultPeerScore
	}
	now := b.currentTime()

	b.mu.Lock()
	defer b.mu.Unlock()

	entry := b.entryLocked(id)
	b.recoverLocked(entry, now)
	return entry.score
}

func (b *PeerReputationBook) currentTime() time.Time {
	if b == nil || b.now == nil {
		return time.Now()
	}
	return b.now()
}

func (b *PeerReputationBook) entryLocked(id peer.ID) *peerReputationEntry {
	entry, ok := b.entries[id]
	if ok {
		return entry
	}
	entry = &peerReputationEntry{
		score:       DefaultPeerScore,
		lastUpdated: b.currentTime(),
	}
	b.entries[id] = entry
	return entry
}

func (b *PeerReputationBook) recoverLocked(entry *peerReputationEntry, now time.Time) {
	if entry == nil {
		return
	}
	if entry.lastUpdated.IsZero() {
		entry.lastUpdated = now
		return
	}
	if b.recoveryWindow <= 0 || b.recoveryStep <= 0 || now.Before(entry.lastUpdated) {
		return
	}
	elapsed := now.Sub(entry.lastUpdated)
	if elapsed < b.recoveryWindow {
		return
	}
	steps := int(elapsed / b.recoveryWindow)
	entry.score += steps * b.recoveryStep
	if entry.score > MaxPeerScore {
		entry.score = MaxPeerScore
	}
	if entry.score > BannedPeerScoreThreshold && !entry.bannedUntil.IsZero() && now.After(entry.bannedUntil) {
		entry.bannedUntil = time.Time{}
	}
	entry.lastUpdated = now
}

func (e *peerReputationEntry) isBanned(now time.Time) bool {
	return !e.bannedUntil.IsZero() && now.Before(e.bannedUntil)
}

func snapshotPeerStatus(id peer.ID, entry *peerReputationEntry, connected bool, now time.Time) PeerReputationStatus {
	status := PeerReputationStatus{
		PeerID:      id.String(),
		Score:       DefaultPeerScore,
		Connected:   connected,
		Successes:   0,
		Penalties:   0,
		LastUpdated: now,
	}
	if entry == nil {
		return status
	}
	status.Score = entry.score
	status.Successes = entry.successes
	status.Penalties = entry.penalties
	status.LastReason = entry.lastReason
	status.LastUpdated = entry.lastUpdated
	status.Banned = entry.isBanned(now)
	if status.Banned {
		status.BannedUntil = entry.bannedUntil
	}
	return status
}

func scaledLimit(base, percent int) int {
	if base <= 0 {
		return 0
	}
	if percent <= 0 {
		return 1
	}
	scaled := (base*percent + 99) / 100
	if scaled < 1 {
		return 1
	}
	return scaled
}
