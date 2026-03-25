package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strconv"
	"strings"
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
	DefaultTaskFailureLimit   = 3
	DefaultQuarantineLimit    = 256
	DefaultMineConcurrency    = 4
	DefaultMempoolTaskBatch   = 8
	DefaultFrontierTaskBatch  = 8
	DefaultTaskTimeout        = 12 * time.Second
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
	Blockchain        *Blockchain
	TxPool            *TxPool
	TaskPool          *TaskPool
	Miner             consensus.Miner
	MinerAddress      string
	Logger            *log.Logger
	MiningInterval    time.Duration
	MineConcurrency   int
	MempoolTaskBatch  int
	FrontierTaskBatch int
	TaskTimeout       time.Duration
	Partitioner       *PeerTaskPartitioner
	PublishBlock      func(context.Context, Block) error
	PublishTx         func(context.Context, Transaction) error

	submitMu sync.Mutex
	mu       sync.Mutex
	inFlight bool

	inFlightTransfers []Transaction
	inFlightTasks     []SearchTaskEnvelope
	taskFailures      map[string]SearchTaskFailure
	quarantinedTasks  []QuarantinedSearchTask
	lastMiningStatus  MiningStatus
}

type MempoolStatus struct {
	PendingTransfers       int    `json:"pendingTransfers"`
	PendingSearchTasks     int    `json:"pendingSearchTasks"`
	PendingFrontierTasks   int    `json:"pendingFrontierTasks"`
	QuarantinedSearchTasks int    `json:"quarantinedSearchTasks"`
	TransferCapacity       int    `json:"transferCapacity"`
	SearchTaskCapacity     int    `json:"searchTaskCapacity"`
	SenderPendingLimit     int    `json:"senderPendingLimit"`
	MiningIntervalSec      int64  `json:"miningIntervalSec"`
	MiningInFlight         bool   `json:"miningInFlight"`
	MinerAddress           string `json:"minerAddress"`
	PartitionEnabled       bool   `json:"partitionEnabled"`
	PartitionWorkerCount   int    `json:"partitionWorkerCount"`
	LastEvaluatedTasks     int    `json:"lastEvaluatedTasks"`
	LastOwnedTasks         int    `json:"lastOwnedTasks"`
	LastSkippedTasks       int    `json:"lastSkippedTasks"`
}

type miningTaskSource string

const (
	miningTaskSourceMempool  miningTaskSource = "mempool"
	miningTaskSourceFrontier miningTaskSource = "frontier"
)

type miningSearchTask struct {
	source   miningTaskSource
	envelope SearchTaskEnvelope
	record   SearchTaskRecord
}

type miningSearchTaskOutcome struct {
	index         int
	workItem      miningSearchTask
	result        consensus.MiningResult
	err           error
	blockedSender bool
	blockedReason string
}

type PendingSearchTask struct {
	Transaction        Transaction       `json:"transaction"`
	Request            SearchTaskRequest `json:"request"`
	TotalBounty        string            `json:"totalBounty"`
	AdjustedDifficulty uint64            `json:"adjustedDifficulty"`
	PrioritySectors    []string          `json:"prioritySectors,omitempty"`
	CreatedAt          time.Time         `json:"createdAt"`
}

type SearchTaskFailure struct {
	Count        int       `json:"count"`
	LastError    string    `json:"lastError"`
	LastOccurred time.Time `json:"lastOccurred"`
}

type QuarantinedSearchTask struct {
	Transaction        Transaction       `json:"transaction"`
	Request            SearchTaskRequest `json:"request"`
	TotalBounty        string            `json:"totalBounty"`
	AdjustedDifficulty uint64            `json:"adjustedDifficulty"`
	PrioritySectors    []string          `json:"prioritySectors,omitempty"`
	CreatedAt          time.Time         `json:"createdAt"`
	FailureCount       int               `json:"failureCount"`
	FailureReason      string            `json:"failureReason"`
	Terminal           bool              `json:"terminal"`
	QuarantinedAt      time.Time         `json:"quarantinedAt"`
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
		Blockchain:        blockchain,
		TxPool:            NewTxPool(),
		TaskPool:          NewTaskPool(),
		Miner:             miner,
		MinerAddress:      minerAddress,
		Logger:            logger,
		MiningInterval:    DefaultMiningInterval,
		MineConcurrency:   DefaultMineConcurrency,
		MempoolTaskBatch:  DefaultMempoolTaskBatch,
		FrontierTaskBatch: DefaultFrontierTaskBatch,
		TaskTimeout:       DefaultTaskTimeout,
		taskFailures:      make(map[string]SearchTaskFailure),
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

func (p *TxPool) Requeue(transactions []Transaction) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, tx := range transactions {
		if _, ok := p.seen[tx.Hash]; ok {
			continue
		}
		key := senderNonceKey(tx.From, tx.Nonce)
		if existingHash, ok := p.bySenderNonce[key]; ok {
			index := p.indexOfHashLocked(existingHash)
			if index >= 0 {
				if !shouldReplace(p.pending[index].Value, tx.Value) {
					continue
				}
				delete(p.seen, existingHash)
				p.pending[index] = tx
				p.seen[tx.Hash] = struct{}{}
				p.bySenderNonce[key] = tx.Hash
				continue
			}
		}
		if p.limit > 0 && len(p.pending) >= p.limit {
			break
		}
		if p.senderLimit > 0 && p.senderCounts[tx.From] >= p.senderLimit {
			continue
		}
		p.pending = append(p.pending, tx)
		p.seen[tx.Hash] = struct{}{}
		p.bySenderNonce[key] = tx.Hash
		p.senderCounts[tx.From]++
	}
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

func (p *TxPool) Snapshot(limit int) []Transaction {
	p.mu.Lock()
	defer p.mu.Unlock()

	return copyTransactions(p.pending, limit)
}

func (p *TxPool) Status() (pending, capacity, senderLimit int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending), p.limit, p.senderLimit
}

func (p *TxPool) RemoveSenderFromNonce(sender string, nonce uint64) []Transaction {
	p.mu.Lock()
	defer p.mu.Unlock()

	removed := make([]Transaction, 0)
	remaining := make([]Transaction, 0, len(p.pending))
	for _, tx := range p.pending {
		if tx.From == sender && tx.Nonce >= nonce {
			removed = append(removed, tx)
			continue
		}
		remaining = append(remaining, tx)
	}
	if len(removed) == 0 {
		return nil
	}
	p.pending = remaining
	p.rebuildIndicesLocked()
	return removed
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

	if limit <= 0 || limit > len(p.pending) {
		limit = len(p.pending)
	}
	if limit == 0 {
		return nil
	}

	bySender := make(map[string][]SearchTaskEnvelope)
	senders := make([]string, 0)
	for _, task := range p.pending {
		sender := task.Transaction.From
		if _, ok := bySender[sender]; !ok {
			senders = append(senders, sender)
		}
		bySender[sender] = append(bySender[sender], task)
	}
	for _, sender := range senders {
		queue := bySender[sender]
		sort.SliceStable(queue, func(i, j int) bool {
			if queue[i].Transaction.Nonce == queue[j].Transaction.Nonce {
				if queue[i].Transaction.Timestamp.Equal(queue[j].Transaction.Timestamp) {
					return queue[i].Transaction.Hash < queue[j].Transaction.Hash
				}
				return queue[i].Transaction.Timestamp.Before(queue[j].Transaction.Timestamp)
			}
			return queue[i].Transaction.Nonce < queue[j].Transaction.Nonce
		})
		bySender[sender] = queue
	}

	selected := make([]SearchTaskEnvelope, 0, limit)
	selectedHashes := make(map[string]struct{}, limit)
	for len(selected) < limit {
		bestSender := ""
		bestIndex := -1
		var bestValue *big.Int
		for index, sender := range senders {
			queue := bySender[sender]
			if len(queue) == 0 {
				continue
			}
			value, _ := queue[0].Transaction.ValueBig()
			if bestIndex < 0 || value.Cmp(bestValue) > 0 {
				bestSender = sender
				bestIndex = index
				bestValue = value
				continue
			}
			if value.Cmp(bestValue) == 0 {
				bestTask := bySender[bestSender][0]
				candidate := queue[0]
				if candidate.Transaction.Timestamp.Before(bestTask.Transaction.Timestamp) ||
					(candidate.Transaction.Timestamp.Equal(bestTask.Transaction.Timestamp) && candidate.Transaction.Hash < bestTask.Transaction.Hash) {
					bestSender = sender
					bestIndex = index
					bestValue = value
				}
			}
		}
		if bestIndex < 0 {
			break
		}
		task := bySender[bestSender][0]
		bySender[bestSender] = bySender[bestSender][1:]
		selected = append(selected, task)
		selectedHashes[task.Transaction.Hash] = struct{}{}
		senders[bestIndex] = bestSender
	}

	remaining := make([]SearchTaskEnvelope, 0, len(p.pending)-len(selected))
	for _, task := range p.pending {
		if _, ok := selectedHashes[task.Transaction.Hash]; ok {
			continue
		}
		remaining = append(remaining, task)
	}
	out := append([]SearchTaskEnvelope(nil), selected...)
	p.pending = remaining
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

func (p *TaskPool) Snapshot(limit int) []SearchTaskEnvelope {
	p.mu.Lock()
	defer p.mu.Unlock()

	if limit <= 0 || limit > len(p.pending) {
		limit = len(p.pending)
	}
	out := make([]SearchTaskEnvelope, 0, limit)
	for _, task := range p.pending[:limit] {
		cloned := task
		cloned.Task = cloneCrawlTask(task.Task)
		out = append(out, cloned)
	}
	return out
}

func (p *TaskPool) Status() (pending, capacity, senderLimit int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending), p.limit, p.senderLimit
}

func (p *TaskPool) RemoveSenderFromNonce(sender string, nonce uint64) []SearchTaskEnvelope {
	p.mu.Lock()
	defer p.mu.Unlock()

	removed := make([]SearchTaskEnvelope, 0)
	remaining := make([]SearchTaskEnvelope, 0, len(p.pending))
	for _, task := range p.pending {
		if task.Transaction.From == sender && task.Transaction.Nonce >= nonce {
			removed = append(removed, task)
			continue
		}
		remaining = append(remaining, task)
	}
	if len(removed) == 0 {
		return nil
	}
	p.pending = remaining
	p.rebuildIndicesLocked()
	return removed
}

func (e *Engine) SubmitTransaction(tx Transaction) (string, error) {
	return e.submitTransaction(tx, true)
}

func (e *Engine) SubmitTransactionFromPeer(tx Transaction) (string, error) {
	return e.submitTransaction(tx, false)
}

func (e *Engine) submitTransaction(tx Transaction, propagate bool) (string, error) {
	if e.Blockchain == nil {
		return "", ErrInvalidBlock
	}
	normalized, err := normalizeTransaction(tx)
	if err != nil {
		return "", err
	}
	e.submitMu.Lock()
	if err := e.validatePendingTransaction(normalized); err != nil {
		e.submitMu.Unlock()
		return "", err
	}
	if err := e.TxPool.Add(normalized); err != nil {
		e.submitMu.Unlock()
		return "", err
	}
	if e.Logger != nil {
		pendingTransfers, _, _ := e.TxPool.Status()
		e.Logger.Printf(
			"accepted transfer tx=%s from=%s to=%s nonce=%d value=%s pendingTransfers=%d",
			normalized.Hash,
			normalized.From,
			normalized.To,
			normalized.Nonce,
			normalized.Value,
			pendingTransfers,
		)
	}
	e.submitMu.Unlock()
	e.publishTransaction(normalized, propagate)
	return normalized.Hash, nil
}

func (e *Engine) SubmitSearchTask(tx Transaction, request SearchTaskRequest) (SearchTaskEnvelope, error) {
	return e.submitSearchTask(tx, request, true)
}

func (e *Engine) SubmitSearchTaskFromPeer(tx Transaction, request SearchTaskRequest) (SearchTaskEnvelope, error) {
	return e.submitSearchTask(tx, request, false)
}

func (e *Engine) submitSearchTask(tx Transaction, request SearchTaskRequest, propagate bool) (SearchTaskEnvelope, error) {
	if e.Blockchain == nil {
		return SearchTaskEnvelope{}, ErrInvalidBlock
	}
	envelope, err := BuildSearchTaskEnvelope(tx, request, e.Miner.PriorityRegistry)
	if err != nil {
		return SearchTaskEnvelope{}, err
	}
	e.submitMu.Lock()
	if err := e.validatePendingTransaction(envelope.Transaction); err != nil {
		e.submitMu.Unlock()
		return SearchTaskEnvelope{}, err
	}
	if err := e.TaskPool.Add(envelope); err != nil {
		e.submitMu.Unlock()
		return SearchTaskEnvelope{}, err
	}
	if e.Logger != nil {
		pendingTasks, _, _ := e.TaskPool.Status()
		e.Logger.Printf(
			"accepted search task tx=%s sender=%s nonce=%d url=%s total=%s difficulty=%d pendingSearchTasks=%d",
			envelope.Transaction.Hash,
			envelope.Transaction.From,
			envelope.Transaction.Nonce,
			envelope.Request.URL,
			envelope.Transaction.Value,
			envelope.Task.AdjustedDifficulty,
			pendingTasks,
		)
	}
	e.submitMu.Unlock()
	e.publishTransaction(envelope.Transaction, propagate)
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

func (e *Engine) MempoolStatus() MempoolStatus {
	txPending, txCapacity, senderLimit := e.TxPool.Status()
	taskPending, taskCapacity, taskSenderLimit := e.TaskPool.Status()
	if taskSenderLimit > senderLimit {
		senderLimit = taskSenderLimit
	}
	frontierPending := 0
	if e != nil && e.Blockchain != nil {
		_, frontierPending = e.Blockchain.PendingSearchTaskSummary()
	}

	e.mu.Lock()
	inFlight := e.inFlight
	quarantined := len(e.quarantinedTasks)
	miningStatus := e.lastMiningStatus
	e.mu.Unlock()

	return MempoolStatus{
		PendingTransfers:       txPending,
		PendingSearchTasks:     taskPending,
		PendingFrontierTasks:   frontierPending,
		QuarantinedSearchTasks: quarantined,
		TransferCapacity:       txCapacity,
		SearchTaskCapacity:     taskCapacity,
		SenderPendingLimit:     senderLimit,
		MiningIntervalSec:      int64(e.MiningInterval / time.Second),
		MiningInFlight:         inFlight,
		MinerAddress:           e.MinerAddress,
		PartitionEnabled:       miningStatus.PartitionEnabled,
		PartitionWorkerCount:   miningStatus.PartitionWorkerCount,
		LastEvaluatedTasks:     miningStatus.LastEvaluatedSearchTasks,
		LastOwnedTasks:         miningStatus.LastOwnedSearchTasks,
		LastSkippedTasks:       miningStatus.LastSkippedSearchTasks,
	}
}

func (e *Engine) MiningStatus() MiningStatus {
	if e == nil {
		return MiningStatus{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	snapshot := e.lastMiningStatus
	snapshot.PartitionWorkers = append([]string(nil), e.lastMiningStatus.PartitionWorkers...)
	if snapshot.MinerAddress == "" {
		snapshot.MinerAddress = e.MinerAddress
	}
	return snapshot
}

func (e *Engine) PendingTransactions(sender string, limit int) []Transaction {
	if e == nil || e.TxPool == nil {
		return nil
	}
	cleaned := strings.TrimSpace(sender)
	if cleaned == "" {
		return e.TxPool.Snapshot(limit)
	}
	return limitTransactions(e.TxPool.PendingForSender(cleaned), limit)
}

func (e *Engine) PendingSearchTasks(sender string, limit int) []PendingSearchTask {
	if e == nil || e.TaskPool == nil {
		return nil
	}
	cleaned := strings.TrimSpace(sender)

	var pending []SearchTaskEnvelope
	if cleaned == "" {
		pending = e.TaskPool.Snapshot(limit)
	} else {
		pending = limitSearchTaskEnvelopes(e.TaskPool.PendingForSender(cleaned), limit)
	}

	out := make([]PendingSearchTask, 0, len(pending))
	for _, envelope := range pending {
		out = append(out, PendingSearchTask{
			Transaction:        envelope.Transaction,
			Request:            envelope.Request,
			TotalBounty:        envelope.Transaction.Value,
			AdjustedDifficulty: envelope.Task.AdjustedDifficulty,
			PrioritySectors:    append([]string(nil), envelope.Task.PrioritySectors...),
			CreatedAt:          envelope.Task.CreatedAt,
		})
	}
	return out
}

func (e *Engine) QuarantinedSearchTasks(sender string, limit int) []QuarantinedSearchTask {
	if e == nil {
		return nil
	}
	cleaned := strings.TrimSpace(sender)

	e.mu.Lock()
	defer e.mu.Unlock()

	items := make([]QuarantinedSearchTask, 0, len(e.quarantinedTasks))
	for index := len(e.quarantinedTasks) - 1; index >= 0; index-- {
		item := e.quarantinedTasks[index]
		if cleaned != "" && item.Transaction.From != cleaned {
			continue
		}
		items = append(items, item)
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return items
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
	if e.Logger != nil {
		pendingTransfers, _, _ := e.TxPool.Status()
		pendingTasks, _, _ := e.TaskPool.Status()
		e.Logger.Printf(
			"mined block number=%d hash=%s txs=%d proofs=%d miner=%s pendingTransfers=%d pendingSearchTasks=%d",
			block.Header.Number,
			block.Hash,
			len(block.Body.Transactions),
			len(block.Body.CrawlProofs),
			block.Header.Miner,
			pendingTransfers,
			pendingTasks,
		)
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
	mempoolBatch := e.MempoolTaskBatch
	if mempoolBatch <= 0 {
		mempoolBatch = DefaultMempoolTaskBatch
	}
	frontierBatch := e.FrontierTaskBatch
	if frontierBatch <= 0 {
		frontierBatch = DefaultFrontierTaskBatch
	}

	taskEnvelopes := e.TaskPool.DrainHighestValue(mempoolBatch)
	workItems := wrapMempoolSearchTasks(taskEnvelopes)
	frontierTasks := e.frontierMiningTasks(frontierBatch)
	workItems = append(workItems, frontierTasks...)
	ownedTaskEnvelopes := append([]SearchTaskEnvelope(nil), taskEnvelopes...)
	miningStatus := MiningStatus{
		MinerAddress:             e.MinerAddress,
		LastEvaluatedSearchTasks: len(workItems),
		LastOwnedSearchTasks:     len(workItems),
		LastOwnedMempoolTasks:    len(taskEnvelopes),
		LastOwnedFrontierTasks:   len(frontierTasks),
		UpdatedAt:                time.Now().UTC(),
	}
	if e.Partitioner != nil {
		var partitioned MiningStatus
		var skipped []miningSearchTask
		workItems, skipped, partitioned = e.Partitioner.FilterWorkItems(workItems)
		partitioned.MinerAddress = e.MinerAddress
		miningStatus = partitioned
		ownedTaskEnvelopes = ownedTaskEnvelopes[:0]
		skippedTaskEnvelopes := make([]SearchTaskEnvelope, 0, len(skipped))
		for _, item := range workItems {
			if item.source == miningTaskSourceMempool {
				ownedTaskEnvelopes = append(ownedTaskEnvelopes, item.envelope)
			}
		}
		for _, item := range skipped {
			if item.source == miningTaskSourceMempool {
				skippedTaskEnvelopes = append(skippedTaskEnvelopes, item.envelope)
			}
		}
		if len(skippedTaskEnvelopes) > 0 {
			e.TaskPool.Requeue(skippedTaskEnvelopes)
		}
		if e.Logger != nil && partitioned.LastSkippedSearchTasks > 0 {
			e.Logger.Printf(
				"mining tick: partition evaluated=%d owned=%d skipped=%d workers=%d local=%s",
				partitioned.LastEvaluatedSearchTasks,
				partitioned.LastOwnedSearchTasks,
				partitioned.LastSkippedSearchTasks,
				partitioned.PartitionWorkerCount,
				partitioned.PartitionLocalWorker,
			)
		}
	}
	e.recordMiningStatus(miningStatus)
	if len(transactions) == 0 && len(workItems) == 0 {
		return Block{}, nil
	}
	if e.Logger != nil {
		e.Logger.Printf(
			"mining tick: drained transfers=%d mempoolSearchTasks=%d frontierSearchTasks=%d",
			len(transactions),
			len(taskEnvelopes),
			len(frontierTasks),
		)
	}
	e.setInFlightWork(transactions, ownedTaskEnvelopes)
	defer e.clearInFlightWork()

	crawlProofs := make([]CrawlProof, 0, len(workItems))
	taskTransactions := make([]Transaction, 0, len(taskEnvelopes))
	minedTaskEnvelopes := make([]SearchTaskEnvelope, 0, len(ownedTaskEnvelopes))
	failed := make([]SearchTaskEnvelope, 0)
	quarantineCutoffs := make(map[string]uint64)
	quarantineReasons := make(map[string]string)
	quarantineFailureCounts := make(map[string]int)
	blockedRequeues := 0
	for _, outcome := range e.mineSearchTasks(ctx, workItems) {
		workItem := outcome.workItem
		envelope := workItem.envelope
		if outcome.blockedSender {
			if workItem.source == miningTaskSourceMempool {
				sender := envelope.Transaction.From
				if cutoff, ok := quarantineCutoffs[sender]; ok && envelope.Transaction.Nonce >= cutoff {
					e.recordQuarantinedTask(
						envelope,
						quarantineFailureCounts[sender],
						fmt.Sprintf("blocked by quarantined nonce %d: %s", cutoff, quarantineReasons[sender]),
						true,
					)
				} else {
					failed = append(failed, envelope)
					blockedRequeues++
				}
			}
			continue
		}
		if outcome.err != nil {
			terminal, reason := classifySearchTaskFailure(outcome.err)
			failure := e.recordTaskFailure(envelope.Transaction.Hash, reason)
			if e.Logger != nil {
				e.Logger.Printf(
					"mining task failed tx=%s nonce=%d url=%s err=%v",
					envelope.Transaction.Hash,
					envelope.Transaction.Nonce,
					envelope.Request.URL,
					outcome.err,
				)
			}
			if terminal || failure.Count >= DefaultTaskFailureLimit {
				if !terminal {
					reason = fmt.Sprintf("max retries reached after %d failures: %s", failure.Count, reason)
				}
				e.recordQuarantinedTask(envelope, failure.Count, reason, terminal)
				e.clearTaskFailure(envelope.Transaction.Hash)
				if workItem.source == miningTaskSourceMempool {
					sender := envelope.Transaction.From
					cutoff, exists := quarantineCutoffs[sender]
					if !exists || envelope.Transaction.Nonce < cutoff {
						quarantineCutoffs[sender] = envelope.Transaction.Nonce
						quarantineReasons[sender] = reason
						quarantineFailureCounts[sender] = failure.Count
					}
				}
				continue
			}
			if workItem.source == miningTaskSourceMempool {
				failed = append(failed, envelope)
			}
			continue
		}
		e.clearTaskFailure(envelope.Transaction.Hash)

		result := outcome.result
		value, _ := new(big.Int).SetString(strings.TrimSpace(workItem.record.GrossBounty), 10)
		if value == nil {
			value = big.NewInt(0)
		}
		architectFee, _ := new(big.Int).SetString(strings.TrimSpace(workItem.record.ArchitectFee), 10)
		if architectFee == nil {
			architectFee = constants.ArchitectFee(value)
		}
		minerReward, _ := new(big.Int).SetString(strings.TrimSpace(workItem.record.MinerReward), 10)
		if minerReward == nil {
			minerReward = new(big.Int).Sub(cloneBigInt(value), cloneBigInt(architectFee))
		}
		crawlProofs = append(crawlProofs, CrawlProof{
			TaskID:       workItem.record.ID,
			TaskTxHash:   firstNonEmptyString(workItem.record.TxHash, workItem.record.ID),
			Query:        workItem.record.Query,
			URL:          result.URL,
			Miner:        e.MinerAddress,
			Page:         result.Page,
			ProofHash:    result.ProofHash,
			GrossBounty:  value.String(),
			ArchitectFee: architectFee.String(),
			MinerReward:  minerReward.String(),
			CreatedAt:    result.CompletedAt,
		})
		if workItem.source == miningTaskSourceMempool {
			taskTransactions = append(taskTransactions, envelope.Transaction)
			minedTaskEnvelopes = append(minedTaskEnvelopes, envelope)
		}
	}

	if len(quarantineCutoffs) > 0 {
		transactions, _ = filterTransactionsBySenderNonceCutoff(transactions, quarantineCutoffs)
		for sender, cutoff := range quarantineCutoffs {
			removedTasks := e.TaskPool.RemoveSenderFromNonce(sender, cutoff)
			for _, removed := range removedTasks {
				e.recordQuarantinedTask(
					removed,
					quarantineFailureCounts[sender],
					fmt.Sprintf("blocked by quarantined nonce %d: %s", cutoff, quarantineReasons[sender]),
					true,
				)
			}
			removedTransfers := e.TxPool.RemoveSenderFromNonce(sender, cutoff)
			if e.Logger != nil && len(removedTransfers) > 0 {
				e.Logger.Printf(
					"quarantined pending transfers sender=%s fromNonce=%d count=%d",
					sender,
					cutoff,
					len(removedTransfers),
				)
			}
		}
	}

	if len(failed) > 0 {
		e.TaskPool.Requeue(failed)
		if e.Logger != nil {
			e.Logger.Printf("mining tick: requeued failed search tasks=%d", len(failed))
		}
	}
	if blockedRequeues > 0 && e.Logger != nil {
		e.Logger.Printf("mining tick: requeued sender-blocked search tasks=%d", blockedRequeues)
	}
	e.setInFlightWork(transactions, minedTaskEnvelopes)

	blockTransactions := append(transactions, taskTransactions...)
	if len(blockTransactions) == 0 && len(crawlProofs) == 0 {
		if e.Logger != nil {
			e.Logger.Printf("mining tick: no block produced after processing drained work")
		}
		return Block{}, nil
	}

	block, err := e.Blockchain.MineBlock(e.MinerAddress, blockTransactions, crawlProofs)
	if err != nil {
		e.TxPool.Requeue(transactions)
		e.TaskPool.Requeue(minedTaskEnvelopes)
		return Block{}, err
	}
	return block, nil
}

func (e *Engine) mineSearchTasks(ctx context.Context, workItems []miningSearchTask) []miningSearchTaskOutcome {
	if len(workItems) == 0 {
		return nil
	}

	type indexedWorkItem struct {
		index    int
		workItem miningSearchTask
	}

	lanesByKey := make(map[string][]indexedWorkItem)
	laneOrder := make([]string, 0, len(workItems))
	for index, workItem := range workItems {
		key := miningLaneKey(workItem)
		if _, ok := lanesByKey[key]; !ok {
			laneOrder = append(laneOrder, key)
		}
		lanesByKey[key] = append(lanesByKey[key], indexedWorkItem{
			index:    index,
			workItem: workItem,
		})
	}

	lanes := make([][]indexedWorkItem, 0, len(laneOrder))
	for _, key := range laneOrder {
		lanes = append(lanes, lanesByKey[key])
	}

	workerCount := e.MineConcurrency
	if workerCount <= 0 {
		workerCount = 1
	}
	if workerCount > len(lanes) {
		workerCount = len(lanes)
	}

	outcomes := make([]miningSearchTaskOutcome, len(workItems))
	jobs := make(chan []indexedWorkItem)
	var wg sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for lane := range jobs {
				blockLaterMempoolTasks := false
				blockedReason := ""
				for _, job := range lane {
					if blockLaterMempoolTasks {
						outcomes[job.index] = miningSearchTaskOutcome{
							index:         job.index,
							workItem:      job.workItem,
							blockedSender: true,
							blockedReason: blockedReason,
						}
						continue
					}

					timeout := e.TaskTimeout
					if timeout <= 0 {
						timeout = DefaultTaskTimeout
					}
					mineCtx, cancel := context.WithTimeout(ctx, timeout)
					result, err := e.Miner.Mine(mineCtx, job.workItem.envelope.Task)
					cancel()
					outcomes[job.index] = miningSearchTaskOutcome{
						index:    job.index,
						workItem: job.workItem,
						result:   result,
						err:      err,
					}
					if err != nil && job.workItem.source == miningTaskSourceMempool {
						blockLaterMempoolTasks = true
						blockedReason = fmt.Sprintf("blocked by failed sender nonce %d", job.workItem.envelope.Transaction.Nonce)
					}
				}
			}
		}()
	}

	for _, lane := range lanes {
		jobs <- lane
	}
	close(jobs)
	wg.Wait()
	return outcomes
}

func (e *Engine) publishTransaction(tx Transaction, propagate bool) {
	if !propagate || e == nil || e.PublishTx == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := e.PublishTx(ctx, tx); err != nil && e.Logger != nil {
		e.Logger.Printf("transaction publish failed tx=%s: %v", tx.Hash, err)
	}
}

func (e *Engine) recordMiningStatus(status MiningStatus) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastMiningStatus = status
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
	inFlightTransfers, inFlightTasks := e.inFlightWorkForSender(sender)
	pending, err := mergeSenderPendingTransactions(
		append(e.TxPool.PendingForSender(sender), inFlightTransfers...),
		append(e.TaskPool.PendingForSender(sender), inFlightTasks...),
	)
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

	bySenderNonce := make(map[string]string, len(merged))
	deduped := make([]Transaction, 0, len(merged))
	for _, tx := range merged {
		key := senderNonceKey(tx.From, tx.Nonce)
		if existingHash, ok := bySenderNonce[key]; ok {
			if existingHash == tx.Hash {
				continue
			}
			return nil, ErrPendingNonceConflict
		}
		bySenderNonce[key] = tx.Hash
		deduped = append(deduped, tx)
	}
	merged = deduped

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

func (e *Engine) setInFlightWork(transactions []Transaction, tasks []SearchTaskEnvelope) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inFlightTransfers = copyTransactions(transactions, 0)
	e.inFlightTasks = limitSearchTaskEnvelopes(tasks, 0)
}

func (e *Engine) clearInFlightWork() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inFlightTransfers = nil
	e.inFlightTasks = nil
}

func (e *Engine) inFlightWorkForSender(sender string) ([]Transaction, []SearchTaskEnvelope) {
	e.mu.Lock()
	defer e.mu.Unlock()

	transfers := make([]Transaction, 0)
	for _, tx := range e.inFlightTransfers {
		if tx.From == sender {
			transfers = append(transfers, tx)
		}
	}

	tasks := make([]SearchTaskEnvelope, 0)
	for _, task := range e.inFlightTasks {
		if task.Transaction.From != sender {
			continue
		}
		cloned := task
		cloned.Task = cloneCrawlTask(task.Task)
		tasks = append(tasks, cloned)
	}
	return transfers, tasks
}

func (e *Engine) recordTaskFailure(hash, reason string) SearchTaskFailure {
	e.mu.Lock()
	defer e.mu.Unlock()

	failure := e.taskFailures[hash]
	failure.Count++
	failure.LastError = reason
	failure.LastOccurred = time.Now().UTC()
	e.taskFailures[hash] = failure
	return failure
}

func (e *Engine) clearTaskFailure(hash string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.taskFailures, hash)
}

func (e *Engine) recordQuarantinedTask(task SearchTaskEnvelope, failureCount int, failureReason string, terminal bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.quarantinedTasks = append(e.quarantinedTasks, QuarantinedSearchTask{
		Transaction:        task.Transaction,
		Request:            task.Request,
		TotalBounty:        task.Transaction.Value,
		AdjustedDifficulty: task.Task.AdjustedDifficulty,
		PrioritySectors:    append([]string(nil), task.Task.PrioritySectors...),
		CreatedAt:          task.Task.CreatedAt,
		FailureCount:       failureCount,
		FailureReason:      failureReason,
		Terminal:           terminal,
		QuarantinedAt:      time.Now().UTC(),
	})
	if len(e.quarantinedTasks) > DefaultQuarantineLimit {
		e.quarantinedTasks = append([]QuarantinedSearchTask(nil), e.quarantinedTasks[len(e.quarantinedTasks)-DefaultQuarantineLimit:]...)
	}
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

func copyTransactions(items []Transaction, limit int) []Transaction {
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	out := make([]Transaction, 0, limit)
	for _, tx := range items[:limit] {
		out = append(out, tx)
	}
	return out
}

func limitTransactions(items []Transaction, limit int) []Transaction {
	return copyTransactions(items, limit)
}

func limitSearchTaskEnvelopes(items []SearchTaskEnvelope, limit int) []SearchTaskEnvelope {
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	out := make([]SearchTaskEnvelope, 0, limit)
	for _, task := range items[:limit] {
		cloned := task
		cloned.Task = cloneCrawlTask(task.Task)
		out = append(out, cloned)
	}
	return out
}

func cloneCrawlTask(task consensus.CrawlTask) consensus.CrawlTask {
	cloned := task
	cloned.SeedURLs = append([]string(nil), task.SeedURLs...)
	cloned.PrioritySectors = append([]string(nil), task.PrioritySectors...)
	cloned.BaseBounty = cloneBigInt(task.BaseBounty)
	cloned.TotalBounty = cloneBigInt(task.TotalBounty)
	cloned.ArchitectFee = cloneBigInt(task.ArchitectFee)
	cloned.MinerReward = cloneBigInt(task.MinerReward)
	return cloned
}

func wrapMempoolSearchTasks(items []SearchTaskEnvelope) []miningSearchTask {
	if len(items) == 0 {
		return nil
	}
	out := make([]miningSearchTask, 0, len(items))
	for _, item := range items {
		out = append(out, miningSearchTask{
			source:   miningTaskSourceMempool,
			envelope: item,
			record:   searchTaskRecordFromEnvelope(item),
		})
	}
	return out
}

func miningLaneKey(item miningSearchTask) string {
	if item.source == miningTaskSourceMempool {
		return "mempool:" + strings.TrimSpace(item.envelope.Transaction.From)
	}
	return "frontier:" + firstNonEmptyString(item.record.ID, item.envelope.Transaction.Hash, item.record.URL)
}

func (e *Engine) frontierMiningTasks(limit int) []miningSearchTask {
	if e == nil || e.Blockchain == nil || limit <= 0 {
		return nil
	}

	quarantined := e.quarantinedTaskIDs()
	records := e.Blockchain.PendingSearchTaskRecords(0)
	out := make([]miningSearchTask, 0, limit)
	for _, record := range records {
		if _, blocked := quarantined[record.ID]; blocked {
			continue
		}
		envelope, err := searchTaskEnvelopeFromRecord(record)
		if err != nil {
			if e.Logger != nil {
				e.Logger.Printf("frontier task skipped id=%s url=%s: %v", record.ID, record.URL, err)
			}
			continue
		}
		out = append(out, miningSearchTask{
			source:   miningTaskSourceFrontier,
			envelope: envelope,
			record:   record,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (e *Engine) quarantinedTaskIDs() map[string]struct{} {
	if e == nil {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	blocked := make(map[string]struct{}, len(e.quarantinedTasks))
	for _, item := range e.quarantinedTasks {
		if hash := strings.TrimSpace(item.Transaction.Hash); hash != "" {
			blocked[hash] = struct{}{}
		}
	}
	return blocked
}

func searchTaskRecordFromEnvelope(envelope SearchTaskEnvelope) SearchTaskRecord {
	architectFee := cloneBigInt(envelope.Task.ArchitectFee)
	minerReward := cloneBigInt(envelope.Task.MinerReward)
	if architectFee.Sign() == 0 || minerReward.Sign() == 0 {
		value, _ := envelope.Transaction.ValueBig()
		if value == nil {
			value = big.NewInt(0)
		}
		architectFee = constants.ArchitectFee(value)
		minerReward = new(big.Int).Sub(cloneBigInt(value), cloneBigInt(architectFee))
	}
	return SearchTaskRecord{
		ID:              firstNonEmptyString(envelope.Task.ID, envelope.Transaction.Hash),
		TxHash:          envelope.Transaction.Hash,
		Submitter:       envelope.Transaction.From,
		Query:           envelope.Request.Query,
		URL:             envelope.Request.URL,
		BaseBounty:      envelope.Request.BaseBounty,
		GrossBounty:     envelope.Transaction.Value,
		ArchitectFee:    architectFee.String(),
		MinerReward:     minerReward.String(),
		Difficulty:      envelope.Task.AdjustedDifficulty,
		DataVolumeBytes: envelope.Request.DataVolumeBytes,
		CreatedAt:       envelope.Task.CreatedAt,
	}
}

func searchTaskEnvelopeFromRecord(record SearchTaskRecord) (SearchTaskEnvelope, error) {
	task, err := crawlTaskFromRecord(record)
	if err != nil {
		return SearchTaskEnvelope{}, err
	}
	return SearchTaskEnvelope{
		Transaction: Transaction{
			Hash:      firstNonEmptyString(record.ID, record.TxHash),
			Type:      TxTypeSearchTask,
			From:      record.Submitter,
			Value:     record.GrossBounty,
			Timestamp: record.CreatedAt,
		},
		Request: SearchTaskRequest{
			Query:           record.Query,
			URL:             record.URL,
			BaseBounty:      record.BaseBounty,
			Difficulty:      record.Difficulty,
			DataVolumeBytes: record.DataVolumeBytes,
		},
		Task: task,
	}, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func classifySearchTaskFailure(err error) (bool, string) {
	if err == nil {
		return false, ""
	}
	reason := strings.TrimSpace(err.Error())
	if reason == "" {
		return false, "unknown crawl failure"
	}
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "crawl returned http 400"),
		strings.Contains(lower, "crawl returned http 401"),
		strings.Contains(lower, "crawl returned http 403"),
		strings.Contains(lower, "crawl returned http 404"),
		strings.Contains(lower, "crawl returned http 410"),
		strings.Contains(lower, "crawl returned http 451"),
		strings.Contains(lower, "already visited"):
		return true, reason
	default:
		return false, reason
	}
}

func filterTransactionsBySenderNonceCutoff(transactions []Transaction, cutoffs map[string]uint64) ([]Transaction, []Transaction) {
	if len(cutoffs) == 0 {
		return transactions, nil
	}
	filtered := make([]Transaction, 0, len(transactions))
	removed := make([]Transaction, 0)
	for _, tx := range transactions {
		cutoff, ok := cutoffs[tx.From]
		if ok && tx.Nonce >= cutoff {
			removed = append(removed, tx)
			continue
		}
		filtered = append(filtered, tx)
	}
	return filtered, removed
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
