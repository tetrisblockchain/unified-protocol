package governance

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"unified/core/constants"
)

type ProposalStatus string

const (
	ProposalStatusActive   ProposalStatus = "active"
	ProposalStatusQueued   ProposalStatus = "queued"
	ProposalStatusPassed   ProposalStatus = "passed"
	ProposalStatusRejected ProposalStatus = "rejected"
	ProposalStatusExecuted ProposalStatus = "executed"
)

type VoteChoice uint8

const (
	VoteAgainst VoteChoice = 0
	VoteFor     VoteChoice = 1
	VoteAbstain VoteChoice = 2
)

var ErrInvalidVoteChoice = errors.New("governance: invalid vote choice")

type ProposalSummary struct {
	ID              uint64         `json:"id"`
	Title           string         `json:"title"`
	TargetComponent string         `json:"targetComponent"`
	LogicExtension  string         `json:"logicExtension"`
	Proposer        string         `json:"proposer"`
	ProposerAlias   string         `json:"proposerAlias"`
	Status          ProposalStatus `json:"status"`
	DeadlineBlock   uint64         `json:"deadlineBlock"`
	ForVotes        string         `json:"forVotes"`
	AgainstVotes    string         `json:"againstVotes"`
	AbstainVotes    string         `json:"abstainVotes"`
}

type GovernanceEvent struct {
	ProposalID    uint64    `json:"proposalId"`
	Sector        string    `json:"sector"`
	MultiplierBPS uint64    `json:"multiplierBps"`
	BlockNumber   uint64    `json:"blockNumber"`
	TransactionID string    `json:"transactionId"`
	EmittedAt     time.Time `json:"emittedAt"`
}

type PriorityRule struct {
	Sector           string    `json:"sector"`
	MultiplierBPS    uint64    `json:"multiplierBps"`
	SourceProposalID uint64    `json:"sourceProposalId"`
	ActivatedAt      time.Time `json:"activatedAt"`
}

type EventSource interface {
	SubscribeGovernanceEvents(ctx context.Context, fromBlock uint64) (<-chan GovernanceEvent, <-chan error, error)
}

type ProposalReader interface {
	ActiveProposals(ctx context.Context) ([]ProposalSummary, error)
	CastVote(ctx context.Context, proposalID uint64, choice VoteChoice) error
}

func (e GovernanceEvent) ToPriorityRule() PriorityRule {
	return PriorityRule{
		Sector:           strings.TrimSpace(strings.ToLower(e.Sector)),
		MultiplierBPS:    normalizeMultiplier(e.MultiplierBPS),
		SourceProposalID: e.ProposalID,
		ActivatedAt:      e.EmittedAt.UTC(),
	}
}

func (v VoteChoice) Uint8() uint8 {
	return uint8(v)
}

func (v VoteChoice) String() string {
	switch v {
	case VoteAgainst:
		return "against"
	case VoteFor:
		return "for"
	case VoteAbstain:
		return "abstain"
	default:
		return "unknown"
	}
}

func ParseVoteChoice(input string) (VoteChoice, error) {
	value := strings.TrimSpace(strings.ToLower(input))
	switch value {
	case "0", "no", "against":
		return VoteAgainst, nil
	case "1", "yes", "for":
		return VoteFor, nil
	case "2", "abstain":
		return VoteAbstain, nil
	default:
		return 0, fmt.Errorf("%w: %s", ErrInvalidVoteChoice, input)
	}
}

func NormalizeProposalTitle(title string, proposalID uint64) string {
	trimmed := strings.TrimSpace(title)
	if trimmed != "" {
		return trimmed
	}

	return "UGP-" + fmt.Sprintf("%03d", proposalID)
}

func FormatVotes(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "0"
	}

	return raw
}

func ProposalQuorumVotes(circulatingSupply string) string {
	total, err := strconv.ParseUint(strings.TrimSpace(circulatingSupply), 10, 64)
	if err != nil {
		return "0"
	}

	return strconv.FormatUint(constants.ScaleUint64Ceil(total, constants.GovernanceQuorumBPS), 10)
}

func normalizeMultiplier(multiplier uint64) uint64 {
	if multiplier == 0 {
		return constants.DefaultMultiplierBPS
	}

	return multiplier
}
