package node

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"

	"unified/core/constants"
)

func TestGovernanceStoreFinalizePublishesEvent(t *testing.T) {
	t.Parallel()

	store, err := NewGovernanceStore(Config{
		StartBlock:          1,
		CirculatingSupply:   big.NewInt(10000),
		OperatorAddress:     "UFI_LOCAL_OPERATOR",
		OperatorAlias:       "local",
		OperatorVotingPower: big.NewInt(5000),
	}, nil)
	if err != nil {
		t.Fatalf("NewGovernanceStore returned error: %v", err)
	}

	events, errs, err := store.SubscribeGovernanceEvents(context.Background(), 0)
	if err != nil {
		t.Fatalf("SubscribeGovernanceEvents returned error: %v", err)
	}

	proposal, err := store.CreateProposal(ProposalCreateRequest{
		Title:           "UGP-001 Prioritize EDU",
		TargetComponent: "pouw.go",
		LogicExtension:  "prioritize .edu domains",
		Sector:          ".edu",
		MultiplierBPS:   15000,
		Stake:           "1000",
	})
	if err != nil {
		t.Fatalf("CreateProposal returned error: %v", err)
	}

	if err := store.CastVote(proposal.ID, 1); err != nil {
		t.Fatalf("CastVote returned error: %v", err)
	}

	store.AdvanceBlocks(constants.GovernanceProposalBlocks + 1)
	result, err := store.FinalizeProposal(proposal.ID)
	if err != nil {
		t.Fatalf("FinalizeProposal returned error: %v", err)
	}
	if result.GovernanceEvent == nil {
		t.Fatalf("expected governance event on finalize")
	}

	select {
	case event := <-events:
		if event.Sector != ".edu" {
			t.Fatalf("event sector = %q, want .edu", event.Sector)
		}
	case err := <-errs:
		t.Fatalf("unexpected event stream error: %v", err)
	}
}

func TestGovernanceStorePersistsAcrossRestart(t *testing.T) {
	t.Parallel()

	storagePath := filepath.Join(t.TempDir(), "governance_state.json")
	config := Config{
		StoragePath:         storagePath,
		StartBlock:          7,
		CirculatingSupply:   big.NewInt(10000),
		OperatorAddress:     "UFI_LOCAL_OPERATOR",
		OperatorAlias:       "local",
		OperatorVotingPower: big.NewInt(5000),
	}

	store, err := NewGovernanceStore(config, nil)
	if err != nil {
		t.Fatalf("NewGovernanceStore returned error: %v", err)
	}

	proposal, err := store.CreateProposal(ProposalCreateRequest{
		Title:           "UGP-002 Prioritize GOV",
		TargetComponent: "pouw.go",
		LogicExtension:  "prioritize .gov domains",
		Sector:          ".gov",
		MultiplierBPS:   13000,
		Stake:           "1000",
	})
	if err != nil {
		t.Fatalf("CreateProposal returned error: %v", err)
	}
	if err := store.CastVote(proposal.ID, 1); err != nil {
		t.Fatalf("CastVote returned error: %v", err)
	}
	store.AdvanceBlocks(10)

	reloaded, err := NewGovernanceStore(config, nil)
	if err != nil {
		t.Fatalf("reload NewGovernanceStore returned error: %v", err)
	}

	if reloaded.CurrentBlock() != 17 {
		t.Fatalf("CurrentBlock = %d, want 17", reloaded.CurrentBlock())
	}
	proposals := reloaded.ListProposals("")
	if len(proposals) != 1 {
		t.Fatalf("proposal count = %d, want 1", len(proposals))
	}
	if proposals[0].Title != "UGP-002 Prioritize GOV" {
		t.Fatalf("proposal title = %q, want persisted title", proposals[0].Title)
	}
	if proposals[0].ForVotes != "5000" {
		t.Fatalf("forVotes = %s, want 5000", proposals[0].ForVotes)
	}
}
