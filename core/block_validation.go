package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"strings"
	"time"

	"unified/core/consensus"
)

const (
	DefaultSyncBatchSize   = 32
	BlockValidationTimeout = 12 * time.Second
)

var ErrForkNotPreferred = errors.New("core: fork not preferred by fork choice")

type BlockImportValidator struct {
	Validators       []consensus.ValidatorNode
	PriorityRegistry *consensus.PriorityRegistry
	ArchitectAddress string
	Logger           *log.Logger
}

func NewLocalBlockImportValidator(crawler consensus.Crawler, registry *consensus.PriorityRegistry, architectAddress string, logger *log.Logger) *BlockImportValidator {
	if logger == nil {
		logger = log.Default()
	}

	validators := make([]consensus.ValidatorNode, 0, 3)
	for index := 1; index <= 3; index++ {
		validators = append(validators, consensus.Validator{
			NodeID:           fmt.Sprintf("validator-%d", index),
			Crawler:          crawler,
			PriorityRegistry: registry,
		})
	}

	return &BlockImportValidator{
		Validators:       validators,
		PriorityRegistry: registry,
		ArchitectAddress: normalizedArchitectAddress(architectAddress),
		Logger:           logger,
	}
}

func (v *BlockImportValidator) ValidateBlock(ctx context.Context, preState *StateSnapshot, block Block) error {
	if v == nil || len(v.Validators) == 0 || len(block.Body.CrawlProofs) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	snapshot := preState.Clone()
	for _, tx := range block.Body.Transactions {
		if _, err := ApplyTransactionWithArchitect(snapshot, tx, v.ArchitectAddress); err != nil {
			return err
		}
	}

	for _, proof := range block.Body.CrawlProofs {
		taskRecord, ok := snapshot.PendingTasks[proof.TaskID]
		if !ok {
			return ErrTaskNotFound
		}

		task, err := crawlTaskFromRecord(taskRecord)
		if err != nil {
			return err
		}
		adjustment, err := v.PriorityRegistry.Apply(task, proof.URL)
		if err != nil {
			return err
		}

		grossBounty, _ := new(big.Int).SetString(taskRecord.GrossBounty, 10)
		architectFee, _ := new(big.Int).SetString(taskRecord.ArchitectFee, 10)
		minerReward, _ := new(big.Int).SetString(taskRecord.MinerReward, 10)
		verifyCtx, cancel := context.WithTimeout(ctx, BlockValidationTimeout)
		result, err := consensus.VerifyMiningResult(
			verifyCtx,
			task,
			consensus.MiningResult{
				TaskID:                 proof.TaskID,
				MinerID:                proof.Miner,
				URL:                    proof.URL,
				Page:                   proof.Page,
				ProofHash:              proof.ProofHash,
				AppliedMultiplierBPS:   adjustment.MultiplierBPS,
				AppliedPrioritySectors: append([]string(nil), adjustment.PrioritySectors...),
				AdjustedDifficulty:     adjustment.AdjustedDifficulty,
				AdjustedBounty:         cloneBigInt(grossBounty),
				ArchitectFee:           cloneBigInt(architectFee),
				MinerReward:            cloneBigInt(minerReward),
				CompletedAt:            proof.CreatedAt,
			},
			v.Validators,
			rand.New(rand.NewSource(int64(block.Header.Number)+int64(len(proof.TaskID)))),
		)
		cancel()
		if err != nil {
			return err
		}
		if !result.Approved {
			if v.Logger != nil {
				v.Logger.Printf("rejecting block %s task %s: similarity quorum failed", block.Hash, proof.TaskID)
			}
			return ErrInvalidBlock
		}

		if _, err := ApplyCrawlProof(snapshot, proof, block.Hash); err != nil {
			return err
		}
	}

	return nil
}

func (bc *Blockchain) Snapshot() (*StateSnapshot, Block) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.state.Clone(), bc.latest
}

func crawlTaskFromRecord(record SearchTaskRecord) (consensus.CrawlTask, error) {
	baseBounty, ok := new(big.Int).SetString(strings.TrimSpace(record.BaseBounty), 10)
	if !ok {
		return consensus.CrawlTask{}, ErrInvalidTransaction
	}

	task, err := consensus.NewCrawlTask(
		record.Query,
		[]string{record.URL},
		baseBounty,
		record.Difficulty,
		record.DataVolumeBytes,
		0,
	)
	if err != nil {
		return consensus.CrawlTask{}, err
	}

	task.ID = record.ID
	task.CreatedAt = record.CreatedAt
	task.TotalBounty, _ = new(big.Int).SetString(record.GrossBounty, 10)
	task.ArchitectFee, _ = new(big.Int).SetString(record.ArchitectFee, 10)
	task.MinerReward, _ = new(big.Int).SetString(record.MinerReward, 10)
	task.Difficulty = record.Difficulty
	task.AdjustedDifficulty = record.Difficulty
	return task, nil
}

func ImportRemoteBlock(ctx context.Context, chain *Blockchain, validator *BlockImportValidator, forkChoice *ForkChoice, block Block) error {
	if chain == nil {
		return ErrInvalidBlock
	}

	queue := []Block{block}
	for len(queue) > 0 {
		candidate := queue[0]
		queue = queue[1:]

		hasBlock := chain.HasBlockHash(candidate.Hash)
		hasParent := candidate.Header.Number == 0 || chain.HasBlockHash(candidate.Header.ParentHash)
		decision := forkChoice.Decide(candidate, hasBlock, hasParent)
		switch decision {
		case ForkDecisionIgnore, ForkDecisionBuffer:
			continue
		case ForkDecisionReject:
			return ErrForkNotPreferred
		case ForkDecisionImport:
			if validator != nil {
				parentState, err := chain.ParentState(candidate)
				if err != nil {
					return err
				}
				if err := validator.ValidateBlock(ctx, parentState, candidate); err != nil {
					return err
				}
			}
			if err := chain.ImportBlock(candidate); err != nil {
				if errors.Is(err, ErrBlockNotFound) {
					continue
				}
				return err
			}
			queue = append(forkChoice.CanonicalImported(candidate), queue...)
		default:
			return ErrInvalidBlock
		}
	}
	return nil
}
