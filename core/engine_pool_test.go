package core

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"testing"

	"unified/core/consensus"
	"unified/core/types"
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

func TestEngineSubmitSearchTaskAllowsQueuedSequentialNonces(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("NewAddressFromPubKey returned error: %v", err)
	}

	chain := openTestChain(t, filepath.Join(t.TempDir(), "chain"), map[string]*big.Int{
		sender.String(): big.NewInt(1_000_000),
	})
	defer chain.Close()

	engine := NewEngine(chain, consensus.Miner{PriorityRegistry: consensus.NewPriorityRegistry()}, "UFI_TEST_MINER", nil)
	baseBounty := big.NewInt(100)
	totalValue, err := consensus.QuoteBounty(baseBounty, 1, 10)
	if err != nil {
		t.Fatalf("QuoteBounty returned error: %v", err)
	}

	for nonce := uint64(0); nonce < 2; nonce++ {
		request := SearchTaskRequest{
			Query:           "initial web seed",
			URL:             fmt.Sprintf("https://example.com/%d", nonce),
			BaseBounty:      baseBounty.String(),
			Difficulty:      1,
			DataVolumeBytes: 10,
		}
		payload, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("Marshal returned error: %v", err)
		}

		tx := Transaction{
			Type:  TxTypeSearchTask,
			From:  sender.String(),
			Value: totalValue.String(),
			Nonce: nonce,
			Data:  payload,
		}
		if err := tx.Sign(privateKey); err != nil {
			t.Fatalf("Sign returned error: %v", err)
		}

		if _, err := engine.SubmitSearchTask(tx, request); err != nil {
			t.Fatalf("SubmitSearchTask nonce %d returned error: %v", nonce, err)
		}
	}

	pendingNonce, err := engine.PendingNonce(sender.String())
	if err != nil {
		t.Fatalf("PendingNonce returned error: %v", err)
	}
	if pendingNonce != 2 {
		t.Fatalf("PendingNonce = %d, want 2", pendingNonce)
	}
	if latest := chain.PendingNonce(sender.String()); latest != 0 {
		t.Fatalf("chain PendingNonce = %d, want 0 before mining", latest)
	}
}

func TestTaskPoolDrainHighestValuePreservesSenderNonceOrder(t *testing.T) {
	t.Parallel()

	pool := NewTaskPool()
	pool.limit = 8
	pool.senderLimit = 8

	tasks := []SearchTaskEnvelope{
		{Transaction: Transaction{Hash: "a-0", From: "UFI_A", Value: "100", Nonce: 0}},
		{Transaction: Transaction{Hash: "a-1", From: "UFI_A", Value: "900", Nonce: 1}},
		{Transaction: Transaction{Hash: "b-0", From: "UFI_B", Value: "500", Nonce: 0}},
	}
	for _, task := range tasks {
		if err := pool.Add(task); err != nil {
			t.Fatalf("Add %s returned error: %v", task.Transaction.Hash, err)
		}
	}

	drained := pool.DrainHighestValue(3)
	if len(drained) != 3 {
		t.Fatalf("drained len = %d, want 3", len(drained))
	}
	if drained[0].Transaction.Hash != "b-0" {
		t.Fatalf("first drained hash = %s, want b-0", drained[0].Transaction.Hash)
	}
	if drained[1].Transaction.Hash != "a-0" {
		t.Fatalf("second drained hash = %s, want a-0", drained[1].Transaction.Hash)
	}
	if drained[2].Transaction.Hash != "a-1" {
		t.Fatalf("third drained hash = %s, want a-1", drained[2].Transaction.Hash)
	}
}

func TestMineOnceRequeuesPendingWorkOnBlockFailure(t *testing.T) {
	t.Parallel()

	senderPublicKey, senderPrivateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey sender returned error: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(senderPublicKey)
	if err != nil {
		t.Fatalf("NewAddressFromPubKey sender returned error: %v", err)
	}
	recipientPublicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey recipient returned error: %v", err)
	}
	recipient, err := types.NewAddressFromPubKey(recipientPublicKey)
	if err != nil {
		t.Fatalf("NewAddressFromPubKey recipient returned error: %v", err)
	}

	chain := openTestChain(t, filepath.Join(t.TempDir(), "chain"), map[string]*big.Int{
		sender.String(): big.NewInt(1_000_000),
	})
	defer chain.Close()

	engine := NewEngine(chain, consensus.Miner{PriorityRegistry: consensus.NewPriorityRegistry()}, "UFI_TEST_MINER", nil)

	invalid := Transaction{
		Type:  TxTypeTransfer,
		From:  sender.String(),
		To:    recipient.String(),
		Value: "100",
		Nonce: 7,
	}
	if err := invalid.Sign(senderPrivateKey); err != nil {
		t.Fatalf("Sign invalid returned error: %v", err)
	}
	if err := engine.TxPool.Add(invalid); err != nil {
		t.Fatalf("TxPool.Add returned error: %v", err)
	}

	if _, err := engine.MineOnce(t.Context()); !errors.Is(err, ErrInvalidNonce) {
		t.Fatalf("MineOnce error = %v, want ErrInvalidNonce", err)
	}

	pending := engine.PendingTransactions(sender.String(), 10)
	if len(pending) != 1 {
		t.Fatalf("pending tx count = %d, want 1", len(pending))
	}
	if pending[0].Hash != invalid.Hash {
		t.Fatalf("pending tx hash = %s, want %s", pending[0].Hash, invalid.Hash)
	}
}

func TestPendingNonceIncludesInFlightSearchTasks(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("NewAddressFromPubKey returned error: %v", err)
	}

	chain := openTestChain(t, filepath.Join(t.TempDir(), "chain"), map[string]*big.Int{
		sender.String(): big.NewInt(1_000_000),
	})
	defer chain.Close()

	engine := NewEngine(chain, consensus.Miner{PriorityRegistry: consensus.NewPriorityRegistry()}, "UFI_TEST_MINER", nil)

	baseBounty := big.NewInt(100)
	totalValue, err := consensus.QuoteBounty(baseBounty, 1, 10)
	if err != nil {
		t.Fatalf("QuoteBounty returned error: %v", err)
	}

	envelopes := make([]SearchTaskEnvelope, 0, 4)
	for nonce := uint64(0); nonce < 4; nonce++ {
		request := SearchTaskRequest{
			Query:           "initial web seed",
			URL:             fmt.Sprintf("https://example.com/%d", nonce),
			BaseBounty:      baseBounty.String(),
			Difficulty:      1,
			DataVolumeBytes: 10,
		}
		payload, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("Marshal returned error: %v", err)
		}

		tx := Transaction{
			Type:  TxTypeSearchTask,
			From:  sender.String(),
			Value: totalValue.String(),
			Nonce: nonce,
			Data:  payload,
		}
		if err := tx.Sign(privateKey); err != nil {
			t.Fatalf("Sign returned error: %v", err)
		}

		envelope, err := BuildSearchTaskEnvelope(tx, request, engine.Miner.PriorityRegistry)
		if err != nil {
			t.Fatalf("BuildSearchTaskEnvelope returned error: %v", err)
		}
		envelopes = append(envelopes, envelope)
	}

	engine.setInFlightWork(nil, envelopes[:2])
	for _, envelope := range envelopes[2:] {
		if err := engine.TaskPool.Add(envelope); err != nil {
			t.Fatalf("TaskPool.Add returned error: %v", err)
		}
	}

	pendingNonce, err := engine.PendingNonce(sender.String())
	if err != nil {
		t.Fatalf("PendingNonce returned error: %v", err)
	}
	if pendingNonce != 4 {
		t.Fatalf("PendingNonce = %d, want 4", pendingNonce)
	}
}
