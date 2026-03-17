package node

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"

	coregov "unified/core/governance"
)

type governanceSnapshot struct {
	CurrentBlock uint64                    `json:"currentBlock"`
	NextProposal uint64                    `json:"nextProposal"`
	Proposals    []proposalRecordSnapshot  `json:"proposals"`
	Events       []coregov.GovernanceEvent `json:"events"`
}

type proposalRecordSnapshot struct {
	ID              uint64 `json:"id"`
	Title           string `json:"title"`
	Proposer        string `json:"proposer"`
	ProposerAlias   string `json:"proposerAlias"`
	TargetComponent string `json:"targetComponent"`
	LogicExtension  string `json:"logicExtension"`
	Sector          string `json:"sector"`
	MultiplierBPS   uint64 `json:"multiplierBps"`
	Stake           string `json:"stake"`
	StartBlock      uint64 `json:"startBlock"`
	EndBlock        uint64 `json:"endBlock"`
	ForVotes        string `json:"forVotes"`
	AgainstVotes    string `json:"againstVotes"`
	AbstainVotes    string `json:"abstainVotes"`
	Executed        bool   `json:"executed"`
	Rejected        bool   `json:"rejected"`
	ArchitectCut    string `json:"architectCut"`
	BurnAmount      string `json:"burnAmount"`
}

func (g *GovernanceStore) load() error {
	if g == nil || g.config.StoragePath == "" {
		return nil
	}

	data, err := os.ReadFile(g.config.StoragePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var snapshot governanceSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.applySnapshotLocked(snapshot)
	return nil
}

func (g *GovernanceStore) mutateLocked(mutate func() error) error {
	before := g.snapshotLocked()
	if err := mutate(); err != nil {
		return err
	}
	if err := g.persistLocked(); err != nil {
		g.applySnapshotLocked(before)
		return err
	}
	return nil
}

func (g *GovernanceStore) persistLocked() error {
	if g == nil || g.config.StoragePath == "" {
		return nil
	}

	payload, err := json.MarshalIndent(g.snapshotLocked(), "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(g.config.StoragePath), 0o755); err != nil {
		return err
	}

	tempPath := g.config.StoragePath + ".tmp"
	if err := os.WriteFile(tempPath, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tempPath, g.config.StoragePath)
}

func (g *GovernanceStore) snapshotLocked() governanceSnapshot {
	snapshot := governanceSnapshot{
		CurrentBlock: g.currentBlock,
		NextProposal: g.nextProposal,
		Proposals:    make([]proposalRecordSnapshot, 0, len(g.proposals)),
		Events:       append([]coregov.GovernanceEvent(nil), g.events...),
	}

	for _, proposal := range g.proposals {
		snapshot.Proposals = append(snapshot.Proposals, proposalRecordSnapshot{
			ID:              proposal.ID,
			Title:           proposal.Title,
			Proposer:        proposal.Proposer,
			ProposerAlias:   proposal.ProposerAlias,
			TargetComponent: proposal.TargetComponent,
			LogicExtension:  proposal.LogicExtension,
			Sector:          proposal.Sector,
			MultiplierBPS:   proposal.MultiplierBPS,
			Stake:           cloneBigInt(proposal.Stake).String(),
			StartBlock:      proposal.StartBlock,
			EndBlock:        proposal.EndBlock,
			ForVotes:        cloneBigInt(proposal.ForVotes).String(),
			AgainstVotes:    cloneBigInt(proposal.AgainstVotes).String(),
			AbstainVotes:    cloneBigInt(proposal.AbstainVotes).String(),
			Executed:        proposal.Executed,
			Rejected:        proposal.Rejected,
			ArchitectCut:    cloneBigInt(proposal.ArchitectCut).String(),
			BurnAmount:      cloneBigInt(proposal.BurnAmount).String(),
		})
	}

	sortProposalSnapshots(snapshot.Proposals)
	return snapshot
}

func (g *GovernanceStore) applySnapshotLocked(snapshot governanceSnapshot) {
	g.currentBlock = snapshot.CurrentBlock
	if g.currentBlock == 0 {
		g.currentBlock = g.config.StartBlock
	}
	g.nextProposal = snapshot.NextProposal
	g.proposals = make(map[uint64]*proposalRecord, len(snapshot.Proposals))
	for _, proposal := range snapshot.Proposals {
		g.proposals[proposal.ID] = &proposalRecord{
			ID:              proposal.ID,
			Title:           proposal.Title,
			Proposer:        proposal.Proposer,
			ProposerAlias:   proposal.ProposerAlias,
			TargetComponent: proposal.TargetComponent,
			LogicExtension:  proposal.LogicExtension,
			Sector:          proposal.Sector,
			MultiplierBPS:   proposal.MultiplierBPS,
			Stake:           mustParseBigInt(proposal.Stake),
			StartBlock:      proposal.StartBlock,
			EndBlock:        proposal.EndBlock,
			ForVotes:        mustParseBigInt(proposal.ForVotes),
			AgainstVotes:    mustParseBigInt(proposal.AgainstVotes),
			AbstainVotes:    mustParseBigInt(proposal.AbstainVotes),
			Executed:        proposal.Executed,
			Rejected:        proposal.Rejected,
			ArchitectCut:    mustParseBigInt(proposal.ArchitectCut),
			BurnAmount:      mustParseBigInt(proposal.BurnAmount),
		}
	}
	g.events = append([]coregov.GovernanceEvent(nil), snapshot.Events...)
}

func sortProposalSnapshots(proposals []proposalRecordSnapshot) {
	for left := 0; left < len(proposals); left++ {
		for right := left + 1; right < len(proposals); right++ {
			if proposals[left].ID > proposals[right].ID {
				proposals[left], proposals[right] = proposals[right], proposals[left]
			}
		}
	}
}

func mustParseBigInt(value string) *big.Int {
	parsed, err := parseBigInt(value)
	if err != nil {
		return big.NewInt(0)
	}
	return parsed
}
