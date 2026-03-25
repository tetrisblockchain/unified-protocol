package core

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"unified/core/consensus"
	"unified/core/constants"
	"unified/core/types"
)

type stubCrawler struct {
	index func(ctx context.Context, task consensus.CrawlTask, targetURL string) (consensus.IndexedPage, error)
}

func (s stubCrawler) Index(ctx context.Context, task consensus.CrawlTask, targetURL string) (consensus.IndexedPage, error) {
	if s.index == nil {
		return consensus.IndexedPage{}, errors.New("stub crawler is not configured")
	}
	return s.index(ctx, task, targetURL)
}

func addSignedSearchTaskToPool(t *testing.T, engine *Engine, privateKey ed25519.PrivateKey, from string, nonce uint64, request SearchTaskRequest) SearchTaskEnvelope {
	t.Helper()

	envelope := makeSignedSearchTaskEnvelope(t, engine, privateKey, from, nonce, request)
	if err := engine.TaskPool.Add(envelope); err != nil {
		t.Fatalf("TaskPool.Add returned error: %v", err)
	}
	return envelope
}

func makeSignedSearchTaskEnvelope(t *testing.T, engine *Engine, privateKey ed25519.PrivateKey, from string, nonce uint64, request SearchTaskRequest) SearchTaskEnvelope {
	t.Helper()
	return makeSignedSearchTaskEnvelopeWithRegistry(t, engine.Miner.PriorityRegistry, privateKey, from, nonce, request)
}

func makeSignedSearchTaskEnvelopeWithRegistry(t *testing.T, registry *consensus.PriorityRegistry, privateKey ed25519.PrivateKey, from string, nonce uint64, request SearchTaskRequest) SearchTaskEnvelope {
	t.Helper()

	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	baseBounty, ok := new(big.Int).SetString(request.BaseBounty, 10)
	if !ok {
		t.Fatalf("invalid base bounty %q", request.BaseBounty)
	}
	totalValue, err := consensus.QuoteBounty(baseBounty, request.Difficulty, request.DataVolumeBytes)
	if err != nil {
		t.Fatalf("QuoteBounty returned error: %v", err)
	}

	tx := Transaction{
		Type:  TxTypeSearchTask,
		From:  from,
		Value: totalValue.String(),
		Nonce: nonce,
		Data:  payload,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}

	envelope, err := BuildSearchTaskEnvelope(tx, request, registry)
	if err != nil {
		t.Fatalf("BuildSearchTaskEnvelope returned error: %v", err)
	}
	return envelope
}

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

func TestMineOnceSkipsUnownedPartitionedTasks(t *testing.T) {
	t.Parallel()

	registry := consensus.NewPriorityRegistry()
	minedByURL := make(map[string]int)
	engine := NewEngine(nil, consensus.Miner{
		ID: "UFI_TEST_MINER",
		Crawler: stubCrawler{index: func(ctx context.Context, task consensus.CrawlTask, targetURL string) (consensus.IndexedPage, error) {
			minedByURL[targetURL]++
			return consensus.IndexedPage{
				URL:         targetURL,
				Title:       "partitioned crawl",
				Body:        "partitioned crawl body",
				Snippet:     "partitioned crawl body",
				ContentHash: "hash-" + targetURL,
				SimHash:     consensus.SimHash(targetURL),
			}, nil
		}},
		PriorityRegistry: registry,
	}, "UFI_TEST_MINER", nil)

	partitioner := NewPeerTaskPartitioner("peer-a", func() []string {
		return []string{"peer-a", "peer-b"}
	})
	engine.Partitioner = partitioner

	var ownedEnvelope SearchTaskEnvelope
	var skippedEnvelope SearchTaskEnvelope
	genesisBalances := make(map[string]*big.Int)
	for index := 0; index < 64 && (ownedEnvelope.Transaction.Hash == "" || skippedEnvelope.Transaction.Hash == ""); index++ {
		publicKey, privateKey, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatalf("GenerateKey returned error: %v", err)
		}
		sender, err := types.NewAddressFromPubKey(publicKey)
		if err != nil {
			t.Fatalf("NewAddressFromPubKey returned error: %v", err)
		}
		candidate := makeSignedSearchTaskEnvelopeWithRegistry(t, registry, privateKey, sender.String(), 0, SearchTaskRequest{
			Query:           "partitioned task",
			URL:             fmt.Sprintf("https://example.com/task/%d", index),
			BaseBounty:      "100",
			Difficulty:      1,
			DataVolumeBytes: 10,
		})
		owner := partitionOwnerForTask(partitioner.workerIDs(), wrapMempoolSearchTasks([]SearchTaskEnvelope{candidate})[0])
		switch {
		case owner == "peer-a" && ownedEnvelope.Transaction.Hash == "":
			ownedEnvelope = candidate
			genesisBalances[sender.String()] = big.NewInt(1_000_000)
		case owner != "peer-a" && skippedEnvelope.Transaction.Hash == "":
			skippedEnvelope = candidate
			genesisBalances[sender.String()] = big.NewInt(1_000_000)
		}
	}
	if ownedEnvelope.Transaction.Hash == "" || skippedEnvelope.Transaction.Hash == "" {
		t.Fatal("failed to find both owned and skipped envelopes")
	}

	chain := openTestChain(t, filepath.Join(t.TempDir(), "chain"), genesisBalances)
	defer chain.Close()
	engine.Blockchain = chain

	if err := engine.TaskPool.Add(ownedEnvelope); err != nil {
		t.Fatalf("TaskPool.Add owned returned error: %v", err)
	}
	if err := engine.TaskPool.Add(skippedEnvelope); err != nil {
		t.Fatalf("TaskPool.Add skipped returned error: %v", err)
	}

	block, err := engine.MineOnce(context.Background())
	if err != nil {
		t.Fatalf("MineOnce returned error: %v", err)
	}
	if len(block.Body.CrawlProofs) != 1 {
		t.Fatalf("crawl proofs = %d, want 1", len(block.Body.CrawlProofs))
	}
	if got := block.Body.CrawlProofs[0].TaskID; got != ownedEnvelope.Task.ID {
		t.Fatalf("mined task ID = %s, want %s", got, ownedEnvelope.Task.ID)
	}
	if got := minedByURL[skippedEnvelope.Request.URL]; got != 0 {
		t.Fatalf("skipped URL mined %d times, want 0", got)
	}

	pending := engine.PendingSearchTasks("", 10)
	if len(pending) != 1 {
		t.Fatalf("pending search tasks = %d, want 1", len(pending))
	}
	if got := pending[0].Transaction.Hash; got != skippedEnvelope.Transaction.Hash {
		t.Fatalf("pending task hash = %s, want %s", got, skippedEnvelope.Transaction.Hash)
	}

	status := engine.MiningStatus()
	if !status.PartitionEnabled {
		t.Fatal("PartitionEnabled = false, want true")
	}
	if status.LastEvaluatedSearchTasks != 2 {
		t.Fatalf("LastEvaluatedSearchTasks = %d, want 2", status.LastEvaluatedSearchTasks)
	}
	if status.LastOwnedSearchTasks != 1 {
		t.Fatalf("LastOwnedSearchTasks = %d, want 1", status.LastOwnedSearchTasks)
	}
	if status.LastSkippedSearchTasks != 1 {
		t.Fatalf("LastSkippedSearchTasks = %d, want 1", status.LastSkippedSearchTasks)
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

func TestMineOnceRunsIndependentSenderTasksConcurrently(t *testing.T) {
	t.Parallel()

	publicKeyA, privateKeyA, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey sender A returned error: %v", err)
	}
	senderA, err := types.NewAddressFromPubKey(publicKeyA)
	if err != nil {
		t.Fatalf("NewAddressFromPubKey sender A returned error: %v", err)
	}
	publicKeyB, privateKeyB, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey sender B returned error: %v", err)
	}
	senderB, err := types.NewAddressFromPubKey(publicKeyB)
	if err != nil {
		t.Fatalf("NewAddressFromPubKey sender B returned error: %v", err)
	}

	chain := openTestChain(t, filepath.Join(t.TempDir(), "chain"), map[string]*big.Int{
		senderA.String(): big.NewInt(1_000_000),
		senderB.String(): big.NewInt(1_000_000),
	})
	defer chain.Close()

	started := make(chan string, 2)
	release := make(chan struct{})

	engine := NewEngine(chain, consensus.Miner{
		Crawler: stubCrawler{index: func(_ context.Context, _ consensus.CrawlTask, targetURL string) (consensus.IndexedPage, error) {
			started <- targetURL
			<-release
			return consensus.IndexedPage{
				URL:         targetURL,
				Title:       "ok",
				Body:        "indexed body",
				Snippet:     "indexed body",
				ContentHash: "hash-" + targetURL,
				SimHash:     99,
				IndexedAt:   time.Now().UTC(),
			}, nil
		}},
		PriorityRegistry: consensus.NewPriorityRegistry(),
	}, "UFI_TEST_MINER", nil)
	engine.MineConcurrency = 2
	engine.MempoolTaskBatch = 2

	addSignedSearchTaskToPool(t, engine, privateKeyA, senderA.String(), 0, SearchTaskRequest{
		Query:           "parallel crawl",
		URL:             "https://example.com/a",
		BaseBounty:      "100",
		Difficulty:      1,
		DataVolumeBytes: 10,
	})
	addSignedSearchTaskToPool(t, engine, privateKeyB, senderB.String(), 0, SearchTaskRequest{
		Query:           "parallel crawl",
		URL:             "https://example.com/b",
		BaseBounty:      "100",
		Difficulty:      1,
		DataVolumeBytes: 10,
	})

	type mineResult struct {
		block Block
		err   error
	}
	done := make(chan mineResult, 1)
	go func() {
		block, err := engine.MineOnce(t.Context())
		done <- mineResult{block: block, err: err}
	}()

	seen := map[string]struct{}{}
	timeout := time.After(2 * time.Second)
	for len(seen) < 2 {
		select {
		case target := <-started:
			seen[target] = struct{}{}
		case <-timeout:
			t.Fatal("expected two independent sender tasks to start before either completed")
		}
	}

	close(release)
	result := <-done
	if result.err != nil {
		t.Fatalf("MineOnce returned error: %v", result.err)
	}
	if result.block.Hash == "" {
		t.Fatal("expected block hash after concurrent mining")
	}
	if len(result.block.Body.CrawlProofs) != 2 {
		t.Fatalf("crawl proofs = %d, want 2", len(result.block.Body.CrawlProofs))
	}
}

func TestMineOnceRequeuesLaterSenderTasksAfterRecoverableFailure(t *testing.T) {
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

	var (
		mu       sync.Mutex
		attempts []string
	)
	engine := NewEngine(chain, consensus.Miner{
		Crawler: stubCrawler{index: func(_ context.Context, _ consensus.CrawlTask, targetURL string) (consensus.IndexedPage, error) {
			mu.Lock()
			attempts = append(attempts, targetURL)
			mu.Unlock()
			if strings.Contains(targetURL, "/0") {
				return consensus.IndexedPage{}, errors.New("temporary upstream timeout")
			}
			return consensus.IndexedPage{
				URL:         targetURL,
				Title:       "ok",
				Body:        "indexed body",
				Snippet:     "indexed body",
				ContentHash: "hash-" + targetURL,
				SimHash:     101,
				IndexedAt:   time.Now().UTC(),
			}, nil
		}},
		PriorityRegistry: consensus.NewPriorityRegistry(),
	}, "UFI_TEST_MINER", nil)
	engine.MineConcurrency = 4
	engine.MempoolTaskBatch = 4

	addSignedSearchTaskToPool(t, engine, privateKey, sender.String(), 0, SearchTaskRequest{
		Query:           "serial sender",
		URL:             "https://example.com/0",
		BaseBounty:      "100",
		Difficulty:      1,
		DataVolumeBytes: 10,
	})
	addSignedSearchTaskToPool(t, engine, privateKey, sender.String(), 1, SearchTaskRequest{
		Query:           "serial sender",
		URL:             "https://example.com/1",
		BaseBounty:      "100",
		Difficulty:      1,
		DataVolumeBytes: 10,
	})

	block, err := engine.MineOnce(t.Context())
	if err != nil {
		t.Fatalf("MineOnce returned error: %v", err)
	}
	if block.Hash != "" {
		t.Fatalf("block hash = %s, want no block when sender lane stalls", block.Hash)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 1 || attempts[0] != "https://example.com/0" {
		t.Fatalf("crawler attempts = %v, want only the first sender URL", attempts)
	}

	pending := engine.PendingSearchTasks(sender.String(), 10)
	if len(pending) != 2 {
		t.Fatalf("pending search tasks = %d, want 2 requeued tasks", len(pending))
	}
}

func TestMineOnceQuarantinesTerminalSearchTaskFailures(t *testing.T) {
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

	engine := NewEngine(chain, consensus.Miner{
		Crawler: stubCrawler{index: func(_ context.Context, _ consensus.CrawlTask, targetURL string) (consensus.IndexedPage, error) {
			if strings.Contains(targetURL, "blocked") {
				return consensus.IndexedPage{}, errors.New("consensus: crawl returned HTTP 403")
			}
			return consensus.IndexedPage{
				URL:         targetURL,
				Title:       "ok",
				Body:        "indexed body",
				ContentHash: "hash-" + targetURL,
				SimHash:     42,
				IndexedAt:   time.Now().UTC(),
			}, nil
		}},
		PriorityRegistry: consensus.NewPriorityRegistry(),
	}, "UFI_TEST_MINER", nil)

	baseBounty := big.NewInt(100)
	totalValue, err := consensus.QuoteBounty(baseBounty, 1, 10)
	if err != nil {
		t.Fatalf("QuoteBounty returned error: %v", err)
	}

	urls := []string{
		"https://example.com/good",
		"https://example.com/blocked",
		"https://example.com/later",
	}
	for nonce, targetURL := range urls {
		request := SearchTaskRequest{
			Query:           "initial web seed",
			URL:             targetURL,
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
			Nonce: uint64(nonce),
			Data:  payload,
		}
		if err := tx.Sign(privateKey); err != nil {
			t.Fatalf("Sign returned error: %v", err)
		}
		if _, err := engine.SubmitSearchTask(tx, request); err != nil {
			t.Fatalf("SubmitSearchTask nonce %d returned error: %v", nonce, err)
		}
	}

	block, err := engine.MineOnce(t.Context())
	if err != nil {
		t.Fatalf("MineOnce returned error: %v", err)
	}
	if block.Hash == "" {
		t.Fatal("MineOnce returned empty block")
	}
	if got := len(block.Body.CrawlProofs); got != 1 {
		t.Fatalf("crawl proofs = %d, want 1", got)
	}
	if pending := engine.PendingSearchTasks(sender.String(), 10); len(pending) != 0 {
		t.Fatalf("pending search tasks = %d, want 0 after quarantine", len(pending))
	}

	quarantined := engine.QuarantinedSearchTasks(sender.String(), 10)
	if len(quarantined) != 2 {
		t.Fatalf("quarantined len = %d, want 2", len(quarantined))
	}
	if !strings.Contains(quarantined[0].FailureReason, "blocked by quarantined nonce 1") && !strings.Contains(quarantined[1].FailureReason, "blocked by quarantined nonce 1") {
		t.Fatalf("quarantine reasons = %+v, want blocked-by-nonce entry", quarantined)
	}
	pendingNonce, err := engine.PendingNonce(sender.String())
	if err != nil {
		t.Fatalf("PendingNonce returned error: %v", err)
	}
	if pendingNonce != 1 {
		t.Fatalf("PendingNonce = %d, want 1", pendingNonce)
	}
	if status := engine.MempoolStatus(); status.QuarantinedSearchTasks != 2 {
		t.Fatalf("QuarantinedSearchTasks = %d, want 2", status.QuarantinedSearchTasks)
	}
}

func TestMineOnceQuarantinesRepeatedSearchTaskFailures(t *testing.T) {
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

	engine := NewEngine(chain, consensus.Miner{
		Crawler: stubCrawler{index: func(_ context.Context, _ consensus.CrawlTask, _ string) (consensus.IndexedPage, error) {
			return consensus.IndexedPage{}, context.DeadlineExceeded
		}},
		PriorityRegistry: consensus.NewPriorityRegistry(),
	}, "UFI_TEST_MINER", nil)

	request := SearchTaskRequest{
		Query:           "initial web seed",
		URL:             "https://slow.example.com",
		BaseBounty:      "100",
		Difficulty:      1,
		DataVolumeBytes: 10,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	totalValue, err := consensus.QuoteBounty(big.NewInt(100), 1, 10)
	if err != nil {
		t.Fatalf("QuoteBounty returned error: %v", err)
	}
	tx := Transaction{
		Type:  TxTypeSearchTask,
		From:  sender.String(),
		Value: totalValue.String(),
		Nonce: 0,
		Data:  payload,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	if _, err := engine.SubmitSearchTask(tx, request); err != nil {
		t.Fatalf("SubmitSearchTask returned error: %v", err)
	}

	for attempt := 0; attempt < DefaultTaskFailureLimit; attempt++ {
		block, err := engine.MineOnce(t.Context())
		if err != nil {
			t.Fatalf("MineOnce attempt %d returned error: %v", attempt+1, err)
		}
		if block.Hash != "" {
			t.Fatalf("MineOnce attempt %d produced block %s, want empty", attempt+1, block.Hash)
		}
	}

	if pending := engine.PendingSearchTasks(sender.String(), 10); len(pending) != 0 {
		t.Fatalf("pending search tasks = %d, want 0 after retry quarantine", len(pending))
	}
	quarantined := engine.QuarantinedSearchTasks(sender.String(), 10)
	if len(quarantined) != 1 {
		t.Fatalf("quarantined len = %d, want 1", len(quarantined))
	}
	if !strings.Contains(quarantined[0].FailureReason, "max retries reached") {
		t.Fatalf("quarantine reason = %q, want max retries", quarantined[0].FailureReason)
	}
}

func TestMineOnceProcessesAutonomousFrontierTasks(t *testing.T) {
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

	request := SearchTaskRequest{
		Query:           "autonomous frontier",
		URL:             "https://example.com",
		BaseBounty:      "100",
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
		Value: "110",
		Nonce: 0,
		Data:  payload,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}

	envelope, err := BuildSearchTaskEnvelope(tx, request, consensus.NewPriorityRegistry())
	if err != nil {
		t.Fatalf("BuildSearchTaskEnvelope returned error: %v", err)
	}

	grossBounty, _ := new(big.Int).SetString(envelope.Transaction.Value, 10)
	architectFee := constants.ArchitectFee(grossBounty)
	minerReward := new(big.Int).Sub(new(big.Int).Set(grossBounty), architectFee)
	rootProof := CrawlProof{
		TaskID:     envelope.Transaction.Hash,
		TaskTxHash: envelope.Transaction.Hash,
		Query:      request.Query,
		URL:        request.URL,
		Miner:      "UFI_TEST_MINER",
		Page: consensus.IndexedPage{
			URL:           request.URL,
			Title:         "Example Domain",
			Body:          "Root content",
			Snippet:       "Root content",
			ContentHash:   "root-hash",
			SimHash:       42,
			OutboundLinks: []string{"https://example.com/docs"},
		},
		GrossBounty:  grossBounty.String(),
		ArchitectFee: architectFee.String(),
		MinerReward:  minerReward.String(),
		CreatedAt:    time.Now().UTC(),
	}

	if _, err := chain.MineBlock("UFI_TEST_MINER", []Transaction{envelope.Transaction}, []CrawlProof{rootProof}); err != nil {
		t.Fatalf("MineBlock returned error: %v", err)
	}

	engine := NewEngine(chain, consensus.Miner{
		Crawler: stubCrawler{index: func(_ context.Context, _ consensus.CrawlTask, targetURL string) (consensus.IndexedPage, error) {
			return consensus.IndexedPage{
				URL:         targetURL,
				Title:       "Autonomous child",
				Body:        "Child content",
				Snippet:     "Child content",
				ContentHash: "child-hash",
				SimHash:     77,
			}, nil
		}},
		PriorityRegistry: consensus.NewPriorityRegistry(),
	}, "UFI_TEST_MINER", nil)

	if status := engine.MempoolStatus(); status.PendingFrontierTasks != 1 {
		t.Fatalf("pending frontier tasks = %d, want 1", status.PendingFrontierTasks)
	}

	block, err := engine.MineOnce(t.Context())
	if err != nil {
		t.Fatalf("MineOnce returned error: %v", err)
	}
	if block.Hash == "" {
		t.Fatal("expected frontier mining to produce a block")
	}
	if len(block.Body.Transactions) != 0 {
		t.Fatalf("block transactions = %d, want 0 for frontier-only mining", len(block.Body.Transactions))
	}
	if len(block.Body.CrawlProofs) != 1 {
		t.Fatalf("crawl proofs = %d, want 1", len(block.Body.CrawlProofs))
	}

	proof := block.Body.CrawlProofs[0]
	if proof.TaskTxHash != envelope.Transaction.Hash {
		t.Fatalf("proof task tx hash = %s, want %s", proof.TaskTxHash, envelope.Transaction.Hash)
	}
	if proof.URL != "https://example.com/docs" {
		t.Fatalf("proof URL = %s, want child URL", proof.URL)
	}
}
