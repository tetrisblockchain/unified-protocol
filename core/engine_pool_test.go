package core

import (
	"errors"
	"testing"
)

func TestTxPoolReplacementRequiresPriceBump(t *testing.T) {
	t.Parallel()

	pool := NewTxPool()
	pool.limit = 8
	pool.senderLimit = 2

	original := Transaction{Hash: "tx-1", From: "UFI_A", Value: "100", Nonce: 0}
	if err := pool.Add(original); err != nil {
		t.Fatalf("Add original returned error: %v", err)
	}

	underpriced := Transaction{Hash: "tx-2", From: "UFI_A", Value: "109", Nonce: 0}
	if err := pool.Add(underpriced); !errors.Is(err, ErrReplacementUnderpriced) {
		t.Fatalf("Add underpriced replacement error = %v, want ErrReplacementUnderpriced", err)
	}

	replacement := Transaction{Hash: "tx-3", From: "UFI_A", Value: "110", Nonce: 0}
	if err := pool.Add(replacement); err != nil {
		t.Fatalf("Add replacement returned error: %v", err)
	}

	drained := pool.Drain(10)
	if len(drained) != 1 {
		t.Fatalf("drained len = %d, want 1", len(drained))
	}
	if drained[0].Hash != replacement.Hash {
		t.Fatalf("drained hash = %s, want %s", drained[0].Hash, replacement.Hash)
	}
}

func TestTaskPoolEnforcesSenderAndGlobalLimits(t *testing.T) {
	t.Parallel()

	senderLimited := NewTaskPool()
	senderLimited.limit = 4
	senderLimited.senderLimit = 1

	first := SearchTaskEnvelope{Transaction: Transaction{Hash: "task-1", From: "UFI_A", Value: "100", Nonce: 0}}
	if err := senderLimited.Add(first); err != nil {
		t.Fatalf("Add first task returned error: %v", err)
	}
	secondSameSender := SearchTaskEnvelope{Transaction: Transaction{Hash: "task-2", From: "UFI_A", Value: "200", Nonce: 1}}
	if err := senderLimited.Add(secondSameSender); !errors.Is(err, ErrSenderQueueFull) {
		t.Fatalf("Add second sender task error = %v, want ErrSenderQueueFull", err)
	}

	globallyLimited := NewTaskPool()
	globallyLimited.limit = 1
	globallyLimited.senderLimit = 4
	if err := globallyLimited.Add(first); err != nil {
		t.Fatalf("Add globally-limited first task returned error: %v", err)
	}
	secondSender := SearchTaskEnvelope{Transaction: Transaction{Hash: "task-3", From: "UFI_B", Value: "100", Nonce: 0}}
	if err := globallyLimited.Add(secondSender); !errors.Is(err, ErrPoolFull) {
		t.Fatalf("Add globally-limited second task error = %v, want ErrPoolFull", err)
	}
}
