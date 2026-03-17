package core

import (
	"context"
	"errors"
	"log"
	"math/big"
	"sort"
	"sync"
	"time"

	"unified/core/consensus"
	"unified/core/constants"
)

const DefaultMiningInterval = 15 * time.Second

var ErrAlreadyMining = errors.New("core: mining already in progress")

type TxPool struct {
	mu      sync.Mutex
	pending []Transaction
	seen    map[string]struct{}
}

type TaskPool struct {
	mu      sync.Mutex
	pending []SearchTaskEnvelope
	seen    map[string]struct{}
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

	mu       sync.Mutex
	inFlight bool
}

func NewTxPool() *TxPool {
	return &TxPool{seen: make(map[string]struct{})}
}

func NewTaskPool() *TaskPool {
	return &TaskPool{seen: make(map[string]struct{})}
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
	p.pending = append(p.pending, tx)
	p.seen[tx.Hash] = struct{}{}
	return nil
}

func (p *TxPool) Drain(limit int) []Transaction {
	p.mu.Lock()
	defer p.mu.Unlock()

	if limit <= 0 || limit > len(p.pending) {
		limit = len(p.pending)
	}
	out := append([]Transaction(nil), p.pending[:limit]...)
	for _, tx := range out {
		delete(p.seen, tx.Hash)
	}
	p.pending = append([]Transaction(nil), p.pending[limit:]...)
	return out
}

func (p *TaskPool) Add(task SearchTaskEnvelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.seen[task.Transaction.Hash]; ok {
		return nil
	}
	p.pending = append(p.pending, task)
	p.seen[task.Transaction.Hash] = struct{}{}
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
	for _, task := range out {
		delete(p.seen, task.Transaction.Hash)
	}
	p.pending = append([]SearchTaskEnvelope(nil), p.pending[limit:]...)
	return out
}

func (p *TaskPool) Requeue(tasks []SearchTaskEnvelope) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, task := range tasks {
		if _, ok := p.seen[task.Transaction.Hash]; ok {
			continue
		}
		p.pending = append(p.pending, task)
		p.seen[task.Transaction.Hash] = struct{}{}
	}
}

func (e *Engine) SubmitTransaction(tx Transaction) (string, error) {
	if e.Blockchain == nil {
		return "", ErrInvalidBlock
	}
	normalized, err := normalizeTransaction(tx)
	if err != nil {
		return "", err
	}
	if err := e.Blockchain.ValidateTransaction(normalized); err != nil {
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
	if err := e.Blockchain.ValidateTransaction(envelope.Transaction); err != nil {
		return SearchTaskEnvelope{}, err
	}
	if err := e.TaskPool.Add(envelope); err != nil {
		return SearchTaskEnvelope{}, err
	}
	return envelope, nil
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
