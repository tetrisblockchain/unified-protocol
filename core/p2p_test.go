package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unified/core/consensus"
)

type staticChainProvider struct {
	latest Block
	blocks map[uint64]Block
}

func (s staticChainProvider) LatestBlock() Block {
	return s.latest
}

func (s staticChainProvider) GetBlockByNumber(number uint64) (Block, error) {
	return s.blocks[number], nil
}

func TestP2PNodeSyncChain(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	block := Block{
		Hash: "block-1",
		Header: BlockHeader{
			Number:     1,
			ParentHash: "genesis",
		},
	}
	provider := staticChainProvider{
		latest: block,
		blocks: map[uint64]Block{1: block},
	}

	source, err := NewP2PNode(ctx, P2PConfig{}, nil, provider, nil)
	if err != nil {
		t.Fatalf("NewP2PNode source: %v", err)
	}
	defer source.Close()

	target, err := NewP2PNode(ctx, P2PConfig{Bootnodes: source.Addresses()}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewP2PNode target: %v", err)
	}
	defer target.Close()

	imported := make([]Block, 0, 1)
	if err := target.SyncChain(ctx, 0, func(block Block) error {
		imported = append(imported, block)
		return nil
	}); err != nil {
		t.Fatalf("SyncChain returned error: %v", err)
	}

	if len(imported) != 1 {
		t.Fatalf("imported blocks = %d, want 1", len(imported))
	}
	if imported[0].Hash != block.Hash {
		t.Fatalf("imported hash = %s, want %s", imported[0].Hash, block.Hash)
	}
}

func TestP2PNodeSyncChainSplitsLargeResponses(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	largeBody := strings.Repeat("x", 3<<20)
	makeBlock := func(number uint64, parentHash string) Block {
		return Block{
			Hash: blockHash(number),
			Header: BlockHeader{
				Number:     number,
				ParentHash: parentHash,
			},
			Body: BlockBody{
				CrawlProofs: []CrawlProof{
					{
						TaskID:     "task-large",
						TaskTxHash: "tx-large",
						URL:        "https://example.com/large",
						Page: consensus.IndexedPage{
							URL:   "https://example.com/large",
							Title: "Large Page",
							Body:  largeBody,
						},
					},
				},
			},
		}
	}

	block1 := makeBlock(1, "genesis")
	block2 := makeBlock(2, block1.Hash)
	provider := staticChainProvider{
		latest: block2,
		blocks: map[uint64]Block{
			1: block1,
			2: block2,
		},
	}

	source, err := NewP2PNode(ctx, P2PConfig{}, nil, provider, nil)
	if err != nil {
		t.Fatalf("NewP2PNode source: %v", err)
	}
	defer source.Close()

	target, err := NewP2PNode(ctx, P2PConfig{Bootnodes: source.Addresses()}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewP2PNode target: %v", err)
	}
	defer target.Close()

	var imported []Block
	if err := target.SyncChain(ctx, 0, func(block Block) error {
		imported = append(imported, block)
		return nil
	}); err != nil {
		t.Fatalf("SyncChain returned error: %v", err)
	}

	if len(imported) != 2 {
		t.Fatalf("imported blocks = %d, want 2", len(imported))
	}
	if imported[0].Hash != block1.Hash || imported[1].Hash != block2.Hash {
		t.Fatalf("unexpected import order: got %s then %s", imported[0].Hash, imported[1].Hash)
	}
}

func blockHash(number uint64) string {
	return "block-large-" + string(rune('a'+number-1))
}

func TestP2PNodePersistsIdentityAcrossRestart(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	identityPath := filepath.Join(t.TempDir(), "p2p", "identity.key")
	first, err := NewP2PNode(ctx, P2PConfig{IdentityKeyPath: identityPath}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewP2PNode first: %v", err)
	}
	firstID := first.host.ID().String()
	if err := first.Close(); err != nil {
		t.Fatalf("Close first returned error: %v", err)
	}

	second, err := NewP2PNode(ctx, P2PConfig{IdentityKeyPath: identityPath}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewP2PNode second: %v", err)
	}
	defer second.Close()

	secondID := second.host.ID().String()
	if secondID != firstID {
		t.Fatalf("peer id after restart = %s, want %s", secondID, firstID)
	}
}

func TestP2PNodeGossipsTransactions(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	source, err := NewP2PNode(ctx, P2PConfig{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewP2PNode source: %v", err)
	}
	defer source.Close()

	received := make(chan Transaction, 4)
	target, err := NewP2PNode(ctx, P2PConfig{Bootnodes: source.Addresses()}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewP2PNode target: %v", err)
	}
	defer target.Close()
	target.SetTransactionHandler(func(tx Transaction) error {
		select {
		case received <- tx:
		default:
		}
		return nil
	})

	source.Start(ctx)
	target.Start(ctx)

	tx := Transaction{
		Hash:  "tx-gossip-1",
		Type:  TxTypeTransfer,
		From:  "UFI_GOSSIP_A",
		To:    "UFI_GOSSIP_B",
		Value: "42",
		Nonce: 0,
	}

	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case got := <-received:
			if got.Hash != tx.Hash {
				t.Fatalf("received hash = %s, want %s", got.Hash, tx.Hash)
			}
			return
		case <-ticker.C:
			if err := source.PublishTransaction(ctx, tx); err != nil {
				t.Fatalf("PublishTransaction returned error: %v", err)
			}
		case <-deadline:
			t.Fatal("timed out waiting for gossiped transaction")
		}
	}
}
