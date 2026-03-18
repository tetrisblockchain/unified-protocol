package core

import (
	"context"
	"errors"
	"log"
	"math/big"
	"sort"
	"strconv"
	"sync"
	"time"

	"unified/core/consensus"
	"unified/core/constants"
)

const DefaultMiningInterval = 15 * time.Second

const (
	DefaultTxPoolLimit        = 4096
	DefaultTaskPoolLimit      = 1024
	DefaultSenderPendingLimit = 32
	DefaultReplacementBumpBPS = 11000
)

var (
	ErrAlreadyMining          = errors.New("core: mining already in progress")
	ErrPoolFull               = errors.New("core: mempool is full")
	ErrSenderQueueFull        = errors.New("core: sender pending limit reached")
	ErrReplacementUnderpriced = errors.New("core: replacement transaction underpriced")
	ErrPendingNonceConflict   = errors.New("core: pending nonce conflict")
)

type TxPool struct {
	mu            sync.Mutex
	pending       []Transaction
	seen          map[string]struct{}
	bySenderNonce map[string]string
	senderCounts  map[string]int
	limit         int
	senderLimit   int
}

type TaskPool struct {
	mu            sync.Mutex
	pending       []SearchTaskEnvelope
	seen          map[string]struct{}
	bySenderNonce map[string]string
	senderCounts  map[string]int
	limit         int
	senderLimit   int
}

type Engine struct {
	Blockchain     *Blockchain
	TxPool         *TxPool
	TaskPool       *TaskPool
	Miner          consensus.Miner
	MinerAddress   string
	Logger         *log.Logger
	MiningInterval time.Duration
	PublishBlock   func(context.Context, Block) error

	submitMu sync.Mutex
	mu       sync.Mutex
	inFlight bool
}

func NewTxPool() *TxPool {
	return &TxPool{
		seen:          make(map[string]struct{}),
		bySenderNonce: make(map[string]string),
		senderCounts:  make(map[string]int),
		limit:         DefaultTxPoolLimit,
		senderLimit:   DefaultSenderPendingLimit,
	}
}

func NewTaskPool() *TaskPool {
	return &TaskPool{
		seen:          make(map[string]struct{}),
		bySenderNonce: make(map[string]string),
		senderCounts:  make(map[string]int),
		limit:         DefaultTaskPoolLimit,
		senderLimit:   DefaultSenderPendingLimit,
	}
}

func NewEngine(blockchain *Blockchain, miner consensus.Miner, minerAddress string, logger *log.Logger) *Engine {
	if logger == nil {
		logger = log.Default()
	}
	return &Engine{
		Blockchain:     blockchain,
		TxPool:         NewTxPool(),
		TaskPool:       NewTaskPool(),
		Miner:          miner,
		MinerAddress:   minerAddress,
		Logger:         logger,
		MiningInterval: DefaultMiningInterval,
	}
}

func (p *TxPool) Add(tx Transaction) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.seen[tx.Hash]; ok {
		return nil
	}
	key := senderNonceKey(tx.From, tx.Nonce)
	if existingHash, ok := p.bySenderNonce[key]; ok {
		index := p.indexOfHashLocked(existingHash)
		if index < 0 {
			delete(p.bySenderNonce, key)
		} else {
			existing := p.pending[index]
			if !shouldReplace(existing.Value, tx.Value) {
				return ErrReplacementUnderpriced
			}
			delete(p.seen, existingHash)
			p.pending[index] = tx
			p.seen[tx.Hash] = struct{}{}
			p.bySenderNonce[key] = tx.Hash
			return nil
		}
	}
	if p.limit > 0 && len(p.pending) >= p.limit {
		return ErrPoolFull
	}
	if p.senderLimit > 0 && p.senderCounts[tx.From] >= p.senderLimit {
		return ErrSenderQueueFull
	}
	p.pending = append(p.pending, tx)
	p.seen[tx.Hash] = struct{}{}
	p.bySenderNonce[key] = tx.Hash
	p.senderCounts[tx.From]++
	return nil
}

func (p *TxPool) Drain(limit int) []Transaction {
	p.mu.Lock()
	defer p.mu.Unlock()

	if limit <= 0 || limit > len(p.pending) {
		limit = len(p.pending)
	}
	out := append([]Transaction(nil), p.pending[:limit]...)
	p.pending = append([]Transaction(nil), p.pending[limit:]...)
	p.rebuildIndicesLocked()
	return out
}

func (p *TxPool) PendingForSender(sender string) []Transaction {
	p.mu.Lock()
	defer p.mu.Unlock()

	pending := make([]Transaction, 0, p.senderCounts[sender])
	for _, tx := range p.pending {
		if tx.From == sender {
			pending = append(pending, tx)
		}
	}
	return pending
}

func (p *TaskPool) Add(task SearchTaskEnvelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.seen[task.Transaction.Hash]; ok {
		return nil
	}
	key := senderNonceKey(task.Transaction.From, task.Transaction.Nonce)
	if existingHash, ok := p.bySenderNonce[key]; ok {
		index := p.indexOfHashLocked(existingHash)
		if index < 0 {
			delete(p.bySenderNonce, key)
		} else {
			existing := p.pending[index]
			if !shouldReplace(existing.Transaction.Value, task.Transaction.Value) {
				return ErrReplacementUnderpriced
			}
			delete(p.seen, existingHash)
			p.pending[index] = task
			p.seen[task.Transaction.Hash] = struct{}{}
			p.bySenderNonce[key] = task.Transaction.Hash
			return nil
		}
	}
	if p.limit > 0 && len(p.pending) >= p.limit {
		return ErrPoolFull
	}
	if p.senderLimit > 0 && p.senderCounts[task.Transaction.From] >= p.senderLimit {
		return ErrSenderQueueFull
	}
	p.pending = append(p.pending, task)
	p.seen[task.Transaction.Hash] = struct{}{}
	p.bySenderNonce[key] = task.Transaction.Hash
	p.senderCounts[task.Transaction.From]++
	return nil
}

func (p *TaskPool) DrainHighestValue(limit int) []SearchTaskEnvelope {
	p.mu.Lock()
	defer p.mu.Unlock()

	sort.SliceStable(p.pending, func(i, j int) bool {
		left, _ := p.pending[i].Transaction.ValueBig()
		right, _ := p.pending[j].Transaction.ValueBig()
		return left.Cmp(right) > 0
	})

	if limit <= 0 || limit > len(p.pending) {
		limit = len(p.pending)
	}
	out := append([]SearchTaskEnvelope(nil), p.pending[:limit]...)
	p.pending = append([]SearchTaskEnvelope(nil), p.pending[limit:]...)
	p.rebuildIndicesLocked()
	return out
}

func (p *TaskPool) Requeue(tasks []SearchTaskEnvelope) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, task := range tasks {
		if _, ok := p.seen[task.Transaction.Hash]; ok {
			continue
		}
		key := senderNonceKey(task.Transaction.From, task.Transaction.Nonce)
		if existingHash, ok := p.bySenderNonce[key]; ok {
			index := p.indexOfHashLocked(existingHash)
			if index >= 0 {
				if !shouldReplace(p.pending[index].Transaction.Value, task.Transaction.Value) {
					continue
				}
				delete(p.seen, existingHash)
				p.pending[index] = task
				p.seen[task.Transaction.Hash] = struct{}{}
				p.bySenderNonce[key] = task.Transaction.Hash
				continue
			}
		}
		if p.limit > 0 && len(p.pending) >= p.limit {
			break
		}
		if p.senderLimit > 0 && p.senderCounts[task.Transaction.From] >= p.senderLimit {
			continue
		}
		p.pending = append(p.pending, task)
		p.seen[task.Transaction.Hash] = struct{}{}
		p.bySenderNonce[key] = task.Transaction.Hash
		p.senderCounts[task.Transaction.From]++
	}
}

func (p *TaskPool) PendingForSender(sender string) []SearchTaskEnvelope {
	p.mu.Lock()
	defer p.mu.Unlock()

	pending := make([]SearchTaskEnvelope, 0, p.senderCounts[sender])
	for _, task := range p.pending {
		if task.Transaction.From == sender {
			pending = append(pending, task)
		}
	}
	return pending
}

func (e *Engine) SubmitTransaction(tx Transaction) (string, error) {
	if e.Blockchain == nil {
		return "", ErrInvalidBlock
	}
	normalized, err := normalizeTransaction(tx)
	if err != nil {
		return "", err
	}
	e.submitMu.Lock()
	defer e.submitMu.Unlock()
	if err := e.validatePendingTransaction(normalized); err != nil {
		return "", err
	}
	if err := e.TxPool.Add(normalized); err != nil {
		return "", err
	}
	return normalized.Hash, nil
}

func (e *Engine) SubmitSearchTask(tx Transaction, request SearchTaskRequest) (SearchTaskEnvelope, error) {
	if e.Blockchain == nil {
		return SearchTaskEnvelope{}, ErrInvalidBlock
	}
	envelope, err := BuildSearchTaskEnvelope(tx, request, e.Miner.PriorityRegistry)
	if err != nil {
		return SearchTaskEnvelope{}, err
	}
	e.submitMu.Lock()
	defer e.submitMu.Unlock()
	if err := e.validatePendingTransaction(envelope.Transaction); err != nil {
		return SearchTaskEnvelope{}, err
	}
	if err := e.TaskPool.Add(envelope); err != nil {
		return SearchTaskEnvelope{}, err
	}
	return envelope, nil
}

func (e *Engine) PendingNonce(address string) (uint64, error) {
	if e.Blockchain == nil {
		return 0, ErrInvalidBlock
	}

	snapshot, err := e.pendingStateForSender(address)
	if err != nil {
		return 0, err
	}
	return snapshot.Nonces[address], nil
}

func (e *Engine) StartMining(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	if e.MiningInterval <= 0 {
		e.MiningInterval = DefaultMiningInterval
	}

	ticker := time.NewTicker(e.MiningInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				go e.tryMine(ctx)
			}
		}
	}()
}

func (e *Engine) tryMine(ctx context.Context) {
	e.mu.Lock()
	if e.inFlight {
		e.mu.Unlock()
		return
	}
	e.inFlight = true
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.inFlight = false
		e.mu.Unlock()
	}()

	block, err := e.MineOnce(ctx)
	if err != nil {
		e.Logger.Printf("mining error: %v", err)
		return
	}
	if block.Hash == "" {
		return
	}
	if e.PublishBlock != nil {
		if err := e.PublishBlock(ctx, block); err != nil {
			e.Logger.Printf("block publish failed: %v", err)
		}
	}
}

func (e *Engine) MineOnce(ctx context.Context) (Block, error) {
	if e.Blockchain == nil {
		return Block{}, ErrInvalidBlock
	}

	transactions := e.TxPool.Drain(256)
	taskEnvelopes := e.TaskPool.DrainHighestValue(8)
	if len(transactions) == 0 && len(taskEnvelopes) == 0 {
		return Block{}, nil
	}

	crawlProofs := make([]CrawlProof, 0, len(taskEnvelopes))
	taskTransactions := make([]Transaction, 0, len(taskEnvelopes))
	failed := make([]SearchTaskEnvelope, 0)

	for _, envelope := range taskEnvelopes {
		mineCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		result, err := e.Miner.Mine(mineCtx, envelope.Task)
		cancel()
		if err != nil {
			failed = append(failed, envelope)
			continue
		}

		value, _ := envelope.Transaction.ValueBig()
		architectFee := constants.ArchitectFee(value)
		minerReward := new(big.Int).Sub(cloneBigInt(value), architectFee)
		crawlProofs = append(crawlProofs, CrawlProof{
			TaskID:       envelope.Transaction.Hash,
			TaskTxHash:   envelope.Transaction.Hash,
			Query:        envelope.Request.Query,
			URL:          result.URL,
			Miner:        e.MinerAddress,
			Page:         result.Page,
			ProofHash:    result.ProofHash,
			GrossBounty:  value.String(),
			ArchitectFee: architectFee.String(),
			MinerReward:  minerReward.String(),
			CreatedAt:    result.CompletedAt,
		})
		taskTransactions = append(taskTransactions, envelope.Transaction)
	}

	if len(failed) > 0 {
		e.TaskPool.Requeue(failed)
	}

	blockTransactions := append(transactions, taskTransactions...)
	if len(blockTransactions) == 0 && len(crawlProofs) == 0 {
		return Block{}, nil
	}

	return e.Blockchain.MineBlock(e.MinerAddress, blockTransactions, crawlProofs)
}

func (e *Engine) validatePendingTransaction(tx Transaction) error {
	snapshot, err := e.pendingStateForSender(tx.From)
	if err != nil {
		return err
	}
	_, err = ApplyTransactionWithArchitect(snapshot, tx, e.Blockchain.ArchitectAddress())
	return err
}

func (e *Engine) pendingStateForSender(sender string) (*StateSnapshot, error) {
	if e.Blockchain == nil {
		return nil, ErrInvalidBlock
	}

	snapshot, _ := e.Blockchain.Snapshot()
	pending, err := mergeSenderPendingTransactions(e.TxPool.PendingForSender(sender), e.TaskPool.PendingForSender(sender))
	if err != nil {
		return nil, err
	}
	for _, tx := range pending {
		if _, err := ApplyTransactionWithArchitect(snapshot, tx, e.Blockchain.ArchitectAddress()); err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

func mergeSenderPendingTransactions(transfers []Transaction, tasks []SearchTaskEnvelope) ([]Transaction, error) {
	merged := make([]Transaction, 0, len(transfers)+len(tasks))
	for _, tx := range transfers {
		merged = append(merged, tx)
	}
	for _, task := range tasks {
		merged = append(merged, task.Transaction)
	}

	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Nonce == merged[j].Nonce {
			if merged[i].Timestamp.Equal(merged[j].Timestamp) {
				return merged[i].Hash < merged[j].Hash
			}
			return merged[i].Timestamp.Before(merged[j].Timestamp)
		}
		return merged[i].Nonce < merged[j].Nonce
	})

	for index := 1; index < len(merged); index++ {
		if merged[index-1].Nonce == merged[index].Nonce {
			return nil, ErrPendingNonceConflict
		}
	}
	return merged, nil
}

func (p *TxPool) indexOfHashLocked(hash string) int {
	for index, tx := range p.pending {
		if tx.Hash == hash {
			return index
		}
	}
	return -1
}

func (p *TxPool) rebuildIndicesLocked() {
	p.seen = make(map[string]struct{}, len(p.pending))
	p.bySenderNonce = make(map[string]string, len(p.pending))
	p.senderCounts = make(map[string]int)
	for _, tx := range p.pending {
		p.seen[tx.Hash] = struct{}{}
		p.bySenderNonce[senderNonceKey(tx.From, tx.Nonce)] = tx.Hash
		p.senderCounts[tx.From]++
	}
}

func (p *TaskPool) indexOfHashLocked(hash string) int {
	for index, task := range p.pending {
		if task.Transaction.Hash == hash {
			return index
		}
	}
	return -1
}

func (p *TaskPool) rebuildIndicesLocked() {
	p.seen = make(map[string]struct{}, len(p.pending))
	p.bySenderNonce = make(map[string]string, len(p.pending))
	p.senderCounts = make(map[string]int)
	for _, task := range p.pending {
		p.seen[task.Transaction.Hash] = struct{}{}
		p.bySenderNonce[senderNonceKey(task.Transaction.From, task.Transaction.Nonce)] = task.Transaction.Hash
		p.senderCounts[task.Transaction.From]++
	}
}

func senderNonceKey(sender string, nonce uint64) string {
	return sender + "#" + strconv.FormatUint(nonce, 10)
}

func shouldReplace(currentValue, nextValue string) bool {
	current, ok := new(big.Int).SetString(currentValue, 10)
	if !ok {
		return false
	}
	next, ok := new(big.Int).SetString(nextValue, 10)
	if !ok {
		return false
	}
	required := new(big.Int).Mul(current, new(big.Int).SetUint64(uint64(DefaultReplacementBumpBPS)))
	required.Quo(required, new(big.Int).SetUint64(constants.BasisPoints))
	return next.Cmp(required) >= 0
}
