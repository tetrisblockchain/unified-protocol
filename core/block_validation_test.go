package core

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"unified/core/consensus"
	"unified/core/types"
)

type fakeValidatorNode struct {
	id         string
	page       consensus.IndexedPage
	adjustment consensus.TaskAdjustment
	err        error
	onIndex    func()
}

func (f fakeValidatorNode) ID() string {
	return f.id
}

func (f fakeValidatorNode) Index(context.Context, consensus.CrawlTask, string) (consensus.IndexedPage, error) {
	if f.onIndex != nil {
		f.onIndex()
	}
	if f.err != nil {
		return consensus.IndexedPage{}, f.err
	}
	return f.page, nil
}

func (f fakeValidatorNode) ResolveGovernance(consensus.CrawlTask, string) (consensus.CrawlTask, consensus.TaskAdjustment, error) {
	if f.err != nil {
		return consensus.CrawlTask{}, consensus.TaskAdjustment{}, f.err
	}
	return consensus.CrawlTask{}, f.adjustment, nil
}

func TestBlockImportValidatorValidateBlock(t *testing.T) {
	state, registry, block, page, adjustment := buildBlockValidationFixture(t, 1)

	validator := &BlockImportValidator{
		Validators: []consensus.ValidatorNode{
			fakeValidatorNode{id: "v1", page: page, adjustment: adjustment},
			fakeValidatorNode{id: "v2", page: page, adjustment: adjustment},
			fakeValidatorNode{id: "v3", page: page, adjustment: adjustment},
		},
		PriorityRegistry: registry,
	}

	if err := validator.ValidateBlock(context.Background(), state, block); err != nil {
		t.Fatalf("ValidateBlock returned error: %v", err)
	}

	badPage := page
	badPage.SimHash = 0
	validator.Validators = []consensus.ValidatorNode{
		fakeValidatorNode{id: "v1", page: badPage, adjustment: adjustment},
		fakeValidatorNode{id: "v2", page: badPage, adjustment: adjustment},
		fakeValidatorNode{id: "v3", page: badPage, adjustment: adjustment},
	}
	if err := validator.ValidateBlock(context.Background(), state, block); !errors.Is(err, ErrInvalidBlock) {
		t.Fatalf("ValidateBlock error = %v, want ErrInvalidBlock", err)
	}
}

func TestBlockImportValidatorSampledModeLimitsRecrawls(t *testing.T) {
	state, registry, block, page, adjustment := buildBlockValidationFixture(t, 3)
	var indexCalls atomic.Int32

	validator := &BlockImportValidator{
		Validators: []consensus.ValidatorNode{
			fakeValidatorNode{id: "v1", page: page, adjustment: adjustment, onIndex: func() { indexCalls.Add(1) }},
			fakeValidatorNode{id: "v2", page: page, adjustment: adjustment, onIndex: func() { indexCalls.Add(1) }},
			fakeValidatorNode{id: "v3", page: page, adjustment: adjustment, onIndex: func() { indexCalls.Add(1) }},
		},
		PriorityRegistry: registry,
		Mode:             ValidationModeSampled,
		SampleSize:       1,
	}

	if err := validator.ValidateBlock(context.Background(), state, block); err != nil {
		t.Fatalf("ValidateBlock returned error: %v", err)
	}
	if got := indexCalls.Load(); got != 3 {
		t.Fatalf("Index calls = %d, want 3", got)
	}
}

func TestBlockImportValidatorOpportunisticModeSkipsRecrawls(t *testing.T) {
	state, registry, block, page, adjustment := buildBlockValidationFixture(t, 2)
	var indexCalls atomic.Int32

	validator := &BlockImportValidator{
		Validators: []consensus.ValidatorNode{
			fakeValidatorNode{id: "v1", page: page, adjustment: adjustment, onIndex: func() { indexCalls.Add(1) }},
			fakeValidatorNode{id: "v2", page: page, adjustment: adjustment, onIndex: func() { indexCalls.Add(1) }},
			fakeValidatorNode{id: "v3", page: page, adjustment: adjustment, onIndex: func() { indexCalls.Add(1) }},
		},
		PriorityRegistry: registry,
		Mode:             ValidationModeOpportunistic,
	}

	if err := validator.ValidateBlock(context.Background(), state, block); err != nil {
		t.Fatalf("ValidateBlock returned error: %v", err)
	}
	if got := indexCalls.Load(); got != 0 {
		t.Fatalf("Index calls = %d, want 0", got)
	}
}

func buildBlockValidationFixture(t *testing.T, proofCount int) (*StateSnapshot, *consensus.PriorityRegistry, Block, consensus.IndexedPage, consensus.TaskAdjustment) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate sender key: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("derive sender address: %v", err)
	}

	state := NewStateSnapshot()
	state.Balances[sender.String()] = big.NewInt(10_000_000)
	registry := consensus.NewPriorityRegistry()

	request := SearchTaskRequest{
		Query:           "distributed search",
		URL:             "https://example.edu",
		BaseBounty:      "100",
		Difficulty:      1,
		DataVolumeBytes: 10,
	}
	page := consensus.IndexedPage{
		URL:         request.URL,
		Title:       "Example",
		Body:        "Distributed search result body",
		Snippet:     "Distributed search result body",
		ContentHash: "abc123",
		SimHash:     consensus.SimHash("Example Distributed search result body"),
	}

	transactions := make([]Transaction, 0, proofCount)
	proofs := make([]CrawlProof, 0, proofCount)
	var lastAdjustment consensus.TaskAdjustment

	for index := 0; index < proofCount; index++ {
		payload, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		tx := Transaction{
			Type:  TxTypeSearchTask,
			From:  sender.String(),
			Value: "110",
			Nonce: uint64(index),
			Data:  payload,
		}
		if err := tx.Sign(privateKey); err != nil {
			t.Fatalf("sign tx: %v", err)
		}

		envelope, err := BuildSearchTaskEnvelope(tx, request, registry)
		if err != nil {
			t.Fatalf("build search task envelope: %v", err)
		}
		transactions = append(transactions, envelope.Transaction)

		task, err := crawlTaskFromRecord(SearchTaskRecord{
			ID:              envelope.Transaction.Hash,
			TxHash:          envelope.Transaction.Hash,
			Submitter:       sender.String(),
			Query:           request.Query,
			URL:             request.URL,
			BaseBounty:      request.BaseBounty,
			GrossBounty:     envelope.Transaction.Value,
			ArchitectFee:    envelope.Task.ArchitectFee.String(),
			MinerReward:     envelope.Task.MinerReward.String(),
			Difficulty:      request.Difficulty,
			DataVolumeBytes: request.DataVolumeBytes,
			CreatedAt:       time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("crawlTaskFromRecord: %v", err)
		}
		lastAdjustment, err = registry.Apply(task, request.URL)
		if err != nil {
			t.Fatalf("registry apply: %v", err)
		}

		proofs = append(proofs, CrawlProof{
			TaskID:       envelope.Transaction.Hash,
			TaskTxHash:   envelope.Transaction.Hash,
			Query:        request.Query,
			URL:          request.URL,
			Miner:        "miner-1",
			Page:         page,
			ProofHash:    computeCrawlProofHash(CrawlProof{TaskID: envelope.Transaction.Hash, URL: request.URL, Page: page}),
			GrossBounty:  envelope.Transaction.Value,
			ArchitectFee: envelope.Task.ArchitectFee.String(),
			MinerReward:  envelope.Task.MinerReward.String(),
			CreatedAt:    time.Now().UTC(),
		})
	}

	return state, registry, Block{
		Hash: "block-1",
		Header: BlockHeader{
			Number: 1,
		},
		Body: BlockBody{
			Transactions: transactions,
			CrawlProofs:  proofs,
		},
	}, page, lastAdjustment
}
