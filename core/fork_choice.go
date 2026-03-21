package core

import (
	"sort"
	"sync"
)

type ForkDecision string

const (
	ForkDecisionImport ForkDecision = "import"
	ForkDecisionBuffer ForkDecision = "buffer"
	ForkDecisionIgnore ForkDecision = "ignore"
	ForkDecisionReject ForkDecision = "reject"
)

type ForkChoiceStatus struct {
	BufferedBlocks int `json:"bufferedBlocks"`
}

type ForkChoice struct {
	mu               sync.Mutex
	bufferedByHash   map[string]Block
	bufferedByParent map[string][]string
}

func NewForkChoice(_ Block) *ForkChoice {
	return &ForkChoice{
		bufferedByHash:   make(map[string]Block),
		bufferedByParent: make(map[string][]string),
	}
}

func (f *ForkChoice) Decide(block Block, hasBlock bool, hasParent bool) ForkDecision {
	if f == nil {
		if hasBlock {
			return ForkDecisionIgnore
		}
		if block.Header.Number > 0 && !hasParent {
			return ForkDecisionBuffer
		}
		return ForkDecisionImport
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case hasBlock:
		return ForkDecisionIgnore
	case block.Header.Number > 0 && !hasParent:
		f.bufferLocked(block)
		return ForkDecisionBuffer
	default:
		return ForkDecisionImport
	}
}

func (f *ForkChoice) CanonicalImported(block Block) []Block {
	if f == nil {
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.takeBufferedChildrenLocked(block.Hash)
}

func (f *ForkChoice) Status() ForkChoiceStatus {
	if f == nil {
		return ForkChoiceStatus{}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	return ForkChoiceStatus{
		BufferedBlocks: len(f.bufferedByHash),
	}
}

func (f *ForkChoice) bufferLocked(block Block) {
	if _, ok := f.bufferedByHash[block.Hash]; ok {
		return
	}
	f.bufferedByHash[block.Hash] = block
	f.bufferedByParent[block.Header.ParentHash] = append(f.bufferedByParent[block.Header.ParentHash], block.Hash)
}

func (f *ForkChoice) takeBufferedChildrenLocked(parentHash string) []Block {
	hashes := append([]string(nil), f.bufferedByParent[parentHash]...)
	delete(f.bufferedByParent, parentHash)
	if len(hashes) == 0 {
		return nil
	}

	children := make([]Block, 0, len(hashes))
	for _, hash := range hashes {
		block, ok := f.bufferedByHash[hash]
		if !ok {
			continue
		}
		delete(f.bufferedByHash, hash)
		children = append(children, block)
	}

	sort.Slice(children, func(i, j int) bool {
		if children[i].Header.Number == children[j].Header.Number {
			return children[i].Hash < children[j].Hash
		}
		return children[i].Header.Number < children[j].Header.Number
	})
	return children
}
