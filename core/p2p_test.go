package core

import (
	"context"
	"testing"
	"time"
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
