package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"unified/core/consensus"
	"unified/core/constants"
	coregov "unified/core/governance"
)

var (
	ErrInvalidProposalInput = errors.New("node: invalid proposal input")
	ErrInvalidVoteRequest   = errors.New("node: invalid vote request")
)

type Config struct {
	HTTPAddress         string
	StoragePath         string
	StartBlock          uint64
	CirculatingSupply   *big.Int
	OperatorAddress     string
	OperatorAlias       string
	OperatorVotingPower *big.Int
}

type Service struct {
	config           Config
	governance       *GovernanceStore
	priorityRegistry *consensus.PriorityRegistry
	listener         *consensus.GovernanceListener
	logger           *log.Logger
	server           *http.Server
}

type ProposalCreateRequest struct {
	Title           string `json:"title"`
	ProposerAlias   string `json:"proposerAlias"`
	TargetComponent string `json:"targetComponent"`
	LogicExtension  string `json:"logicExtension"`
	Sector          string `json:"sector"`
	MultiplierBPS   uint64 `json:"multiplierBps"`
	Stake           string `json:"stake"`
}

type ProposalCreateResponse struct {
	Proposal coregov.ProposalSummary `json:"proposal"`
}

type VoteRequest struct {
	ProposalID uint64 `json:"proposalId"`
	Choice     uint8  `json:"choice"`
}

type FinalizeRequest struct {
	ProposalID uint64 `json:"proposalId"`
}

type FinalizeResponse struct {
	ProposalID      uint64                   `json:"proposalId"`
	Status          string                   `json:"status"`
	CurrentBlock    uint64                   `json:"currentBlock"`
	ArchitectCut    string                   `json:"architectCut"`
	SlashedAmount   string                   `json:"slashedAmount"`
	BurnAmount      string                   `json:"burnAmount"`
	GovernanceEvent *coregov.GovernanceEvent `json:"governanceEvent,omitempty"`
}

type AdvanceBlocksRequest struct {
	Blocks uint64 `json:"blocks"`
}

type QuoteRequest struct {
	Query           string `json:"query"`
	URL             string `json:"url"`
	BaseBounty      string `json:"baseBounty"`
	Difficulty      uint64 `json:"difficulty"`
	DataVolumeBytes uint64 `json:"dataVolumeBytes"`
}

type QuoteResponse struct {
	Query              string   `json:"query"`
	URL                string   `json:"url"`
	MultiplierBPS      uint64   `json:"multiplierBps"`
	AdjustedDifficulty uint64   `json:"adjustedDifficulty"`
	AdjustedBounty     string   `json:"adjustedBounty"`
	ArchitectFee       string   `json:"architectFee"`
	MinerReward        string   `json:"minerReward"`
	PrioritySectors    []string `json:"prioritySectors"`
}

type HealthResponse struct {
	Status        string                 `json:"status"`
	CurrentBlock  uint64                 `json:"currentBlock"`
	Operator      string                 `json:"operator"`
	OperatorAlias string                 `json:"operatorAlias"`
	PriorityRules []coregov.PriorityRule `json:"priorityRules"`
}

func NewService(config Config, logger *log.Logger) (*Service, error) {
	normalized, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = log.Default()
	}

	governance, err := NewGovernanceStore(normalized, logger)
	if err != nil {
		return nil, err
	}
	priorityRegistry := consensus.NewPriorityRegistry()
	listener := &consensus.GovernanceListener{
		Source:    governance,
		Registry:  priorityRegistry,
		FromBlock: normalized.StartBlock,
	}

	service := &Service{
		config:           normalized,
		governance:       governance,
		priorityRegistry: priorityRegistry,
		listener:         listener,
		logger:           logger,
	}

	return service, nil
}

func (s *Service) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	mux := http.NewServeMux()
	s.RegisterHandlers(mux)

	server := &http.Server{
		Addr:              s.config.HTTPAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.server = server

	listenerErr := s.StartListener(ctx)

	serverErr := make(chan error, 1)
	go func() {
		s.logger.Printf("unified-node listening on %s", s.config.HTTPAddress)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-listenerErr:
		if err == nil {
			return nil
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return err
	case err := <-serverErr:
		return err
	}
}

func (s *Service) RegisterHandlers(mux *http.ServeMux) {
	if mux == nil {
		return
	}

	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/chain/status", s.handleChainStatus)
	mux.HandleFunc("/chain/advance", s.handleAdvanceBlocks)
	mux.HandleFunc("/governance/proposals", s.handleGovernanceProposals)
	mux.HandleFunc("/governance/vote", s.handleVote)
	mux.HandleFunc("/governance/finalize", s.handleFinalize)
	mux.HandleFunc("/governance/events", s.handleEvents)
	mux.HandleFunc("/governance/rules", s.handleRules)
	mux.HandleFunc("/consensus/quote", s.handleQuote)
}

func (s *Service) StartListener(ctx context.Context) <-chan error {
	errs := make(chan error, 1)
	go func() {
		defer close(errs)
		if err := s.listener.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			errs <- err
		}
	}()
	return errs
}

func (s *Service) GovernanceStore() *GovernanceStore {
	if s == nil {
		return nil
	}

	return s.governance
}

func (s *Service) PriorityRegistry() *consensus.PriorityRegistry {
	if s == nil {
		return nil
	}

	return s.priorityRegistry
}

func (s *Service) handleHealth(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeJSON(writer, http.StatusOK, HealthResponse{
		Status:        "ok",
		CurrentBlock:  s.governance.CurrentBlock(),
		Operator:      s.config.OperatorAddress,
		OperatorAlias: s.config.OperatorAlias,
		PriorityRules: s.priorityRegistry.Snapshot(),
	})
}

func (s *Service) handleChainStatus(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeJSON(writer, http.StatusOK, map[string]any{
		"currentBlock":      s.governance.CurrentBlock(),
		"circulatingSupply": cloneBigInt(s.config.CirculatingSupply).String(),
		"quorumVotes":       s.governance.QuorumVotes().String(),
	})
}

func (s *Service) handleAdvanceBlocks(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var payload AdvanceBlocksRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	if payload.Blocks == 0 {
		payload.Blocks = 1
	}

	currentBlock := s.governance.AdvanceBlocks(payload.Blocks)
	writeJSON(writer, http.StatusAccepted, map[string]any{
		"currentBlock": currentBlock,
		"advancedBy":   payload.Blocks,
	})
}

func (s *Service) handleGovernanceProposals(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		status := request.URL.Query().Get("status")
		writeJSON(writer, http.StatusOK, s.governance.ListProposals(status))
	case http.MethodPost:
		var payload ProposalCreateRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid JSON payload")
			return
		}

		proposal, err := s.governance.CreateProposal(payload)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}

		writeJSON(writer, http.StatusCreated, ProposalCreateResponse{Proposal: proposal})
	default:
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) handleVote(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var payload VoteRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	if _, err := coregov.ParseVoteChoice(strconv.FormatUint(uint64(payload.Choice), 10)); err != nil {
		writeError(writer, http.StatusBadRequest, ErrInvalidVoteRequest.Error())
		return
	}

	if err := s.governance.CastVote(payload.ProposalID, coregov.VoteChoice(payload.Choice)); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(writer, http.StatusAccepted, map[string]any{
		"proposalId": payload.ProposalID,
		"choice":     payload.Choice,
		"status":     "accepted",
	})
}

func (s *Service) handleFinalize(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var payload FinalizeRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	result, err := s.governance.FinalizeProposal(payload.ProposalID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(writer, http.StatusOK, result)
}

func (s *Service) handleEvents(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	fromBlock := uint64(0)
	if raw := strings.TrimSpace(request.URL.Query().Get("fromBlock")); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "fromBlock must be an unsigned integer")
			return
		}
		fromBlock = value
	}

	writeJSON(writer, http.StatusOK, s.governance.EventsSince(fromBlock))
}

func (s *Service) handleRules(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeJSON(writer, http.StatusOK, s.priorityRegistry.Snapshot())
}

func (s *Service) handleQuote(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var payload QuoteRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	baseBounty, err := parseBigInt(payload.BaseBounty)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "baseBounty must be a base-10 integer")
		return
	}

	task, err := consensus.NewCrawlTask(
		payload.Query,
		[]string{payload.URL},
		baseBounty,
		payload.Difficulty,
		payload.DataVolumeBytes,
		0,
	)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}

	adjustment, err := s.priorityRegistry.Apply(task, payload.URL)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(writer, http.StatusOK, QuoteResponse{
		Query:              payload.Query,
		URL:                payload.URL,
		MultiplierBPS:      adjustment.MultiplierBPS,
		AdjustedDifficulty: adjustment.AdjustedDifficulty,
		AdjustedBounty:     cloneBigInt(adjustment.AdjustedBounty).String(),
		ArchitectFee:       cloneBigInt(adjustment.ArchitectFee).String(),
		MinerReward:        cloneBigInt(adjustment.NetMinerReward).String(),
		PrioritySectors:    append([]string(nil), adjustment.PrioritySectors...),
	})
}

func normalizeConfig(config Config) (Config, error) {
	normalized := config
	if strings.TrimSpace(normalized.HTTPAddress) == "" {
		normalized.HTTPAddress = ":8545"
	}
	if normalized.CirculatingSupply == nil || normalized.CirculatingSupply.Sign() <= 0 {
		normalized.CirculatingSupply = big.NewInt(1_000_000)
	}
	if strings.TrimSpace(normalized.OperatorAddress) == "" {
		normalized.OperatorAddress = "UFI_LOCAL_OPERATOR"
	}
	if strings.TrimSpace(normalized.OperatorAlias) == "" {
		normalized.OperatorAlias = "local-operator"
	}
	if normalized.OperatorVotingPower == nil || normalized.OperatorVotingPower.Sign() <= 0 {
		normalized.OperatorVotingPower = big.NewInt(5_000)
	}

	return normalized, nil
}

type GovernanceStore struct {
	config Config
	logger *log.Logger

	mu             sync.RWMutex
	currentBlock   uint64
	nextProposal   uint64
	proposals      map[uint64]*proposalRecord
	events         []coregov.GovernanceEvent
	subscribers    map[uint64]chan coregov.GovernanceEvent
	nextSubscriber uint64
}

type proposalRecord struct {
	ID              uint64
	Title           string
	Proposer        string
	ProposerAlias   string
	TargetComponent string
	LogicExtension  string
	Sector          string
	MultiplierBPS   uint64
	Stake           *big.Int
	StartBlock      uint64
	EndBlock        uint64
	ForVotes        *big.Int
	AgainstVotes    *big.Int
	AbstainVotes    *big.Int
	Executed        bool
	Rejected        bool
	ArchitectCut    *big.Int
	BurnAmount      *big.Int
}

func NewGovernanceStore(config Config, logger *log.Logger) (*GovernanceStore, error) {
	if logger == nil {
		logger = log.Default()
	}

	store := &GovernanceStore{
		config:       config,
		logger:       logger,
		currentBlock: config.StartBlock,
		proposals:    make(map[uint64]*proposalRecord),
		subscribers:  make(map[uint64]chan coregov.GovernanceEvent),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (g *GovernanceStore) CurrentBlock() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.currentBlock
}

func (g *GovernanceStore) QuorumVotes() *big.Int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return new(big.Int).Quo(
		new(big.Int).Mul(cloneBigInt(g.config.CirculatingSupply), new(big.Int).SetUint64(constants.GovernanceQuorumBPS)),
		new(big.Int).SetUint64(constants.BasisPoints),
	)
}

func (g *GovernanceStore) AdvanceBlocks(blocks uint64) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.mutateLocked(func() error {
		g.currentBlock += blocks
		return nil
	}); err != nil && g.logger != nil {
		g.logger.Printf("governance persistence failed after block advance: %v", err)
	}
	return g.currentBlock
}

func (g *GovernanceStore) SetCurrentBlock(block uint64) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.mutateLocked(func() error {
		g.currentBlock = block
		return nil
	}); err != nil && g.logger != nil {
		g.logger.Printf("governance persistence failed after block sync: %v", err)
	}
	return g.currentBlock
}

func (g *GovernanceStore) CreateProposal(input ProposalCreateRequest) (coregov.ProposalSummary, error) {
	title := strings.TrimSpace(input.Title)
	target := strings.TrimSpace(input.TargetComponent)
	logic := strings.TrimSpace(input.LogicExtension)
	sector := strings.TrimSpace(strings.ToLower(input.Sector))
	if title == "" || target == "" || logic == "" || sector == "" || input.MultiplierBPS == 0 {
		return coregov.ProposalSummary{}, ErrInvalidProposalInput
	}
	if !strings.HasPrefix(title, "UGP-") {
		return coregov.ProposalSummary{}, fmt.Errorf("%w: title must use UGP-XXX format", ErrInvalidProposalInput)
	}

	stake, err := parseBigInt(input.Stake)
	if err != nil {
		return coregov.ProposalSummary{}, fmt.Errorf("%w: invalid stake", ErrInvalidProposalInput)
	}

	threshold := big.NewInt(1000)
	if cloneBigInt(g.config.OperatorVotingPower).Cmp(threshold) < 0 && stake.Cmp(threshold) < 0 {
		return coregov.ProposalSummary{}, fmt.Errorf("%w: proposer must control or stake at least 1000 UFD", ErrInvalidProposalInput)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	var summary coregov.ProposalSummary
	if err := g.mutateLocked(func() error {
		g.nextProposal++
		proposalID := g.nextProposal
		record := &proposalRecord{
			ID:              proposalID,
			Title:           title,
			Proposer:        g.config.OperatorAddress,
			ProposerAlias:   firstNonEmpty(strings.TrimSpace(input.ProposerAlias), g.config.OperatorAlias),
			TargetComponent: target,
			LogicExtension:  logic,
			Sector:          sector,
			MultiplierBPS:   input.MultiplierBPS,
			Stake:           stake,
			StartBlock:      g.currentBlock,
			EndBlock:        g.currentBlock + constants.GovernanceProposalBlocks,
			ForVotes:        big.NewInt(0),
			AgainstVotes:    big.NewInt(0),
			AbstainVotes:    big.NewInt(0),
			ArchitectCut:    big.NewInt(0),
			BurnAmount:      big.NewInt(0),
		}
		g.proposals[proposalID] = record
		summary = g.summarize(record, g.currentBlock)
		return nil
	}); err != nil {
		return coregov.ProposalSummary{}, err
	}

	return summary, nil
}

func (g *GovernanceStore) ListProposals(statusFilter string) []coregov.ProposalSummary {
	g.mu.RLock()
	defer g.mu.RUnlock()

	summaries := make([]coregov.ProposalSummary, 0, len(g.proposals))
	for _, proposal := range g.proposals {
		summary := g.summarize(proposal, g.currentBlock)
		if statusFilter != "" && string(summary.Status) != statusFilter {
			continue
		}
		summaries = append(summaries, summary)
	}

	// Stable order for clients.
	for left := 0; left < len(summaries); left++ {
		for right := left + 1; right < len(summaries); right++ {
			if summaries[left].ID > summaries[right].ID {
				summaries[left], summaries[right] = summaries[right], summaries[left]
			}
		}
	}

	return summaries
}

func (g *GovernanceStore) CastVote(proposalID uint64, choice coregov.VoteChoice) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	proposal, ok := g.proposals[proposalID]
	if !ok {
		return fmt.Errorf("node: proposal %d not found", proposalID)
	}
	if g.statusOf(proposal, g.currentBlock) != coregov.ProposalStatusActive {
		return fmt.Errorf("node: proposal %d is not active", proposalID)
	}

	return g.mutateLocked(func() error {
		voteWeight := cloneBigInt(g.config.OperatorVotingPower)
		switch choice {
		case coregov.VoteAgainst:
			proposal.AgainstVotes.Add(proposal.AgainstVotes, voteWeight)
		case coregov.VoteFor:
			proposal.ForVotes.Add(proposal.ForVotes, voteWeight)
		case coregov.VoteAbstain:
			proposal.AbstainVotes.Add(proposal.AbstainVotes, voteWeight)
		default:
			return ErrInvalidVoteRequest
		}
		return nil
	})
}

func (g *GovernanceStore) FinalizeProposal(proposalID uint64) (FinalizeResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	proposal, ok := g.proposals[proposalID]
	if !ok {
		return FinalizeResponse{}, fmt.Errorf("node: proposal %d not found", proposalID)
	}
	if g.currentBlock <= proposal.EndBlock {
		return FinalizeResponse{}, fmt.Errorf("node: proposal %d is still active until block %d", proposalID, proposal.EndBlock)
	}
	if proposal.Executed || proposal.Rejected {
		return FinalizeResponse{}, fmt.Errorf("node: proposal %d already finalized", proposalID)
	}

	participation := new(big.Int).Add(cloneBigInt(proposal.ForVotes), cloneBigInt(proposal.AgainstVotes))
	participation.Add(participation, proposal.AbstainVotes)
	quorum := new(big.Int).Quo(
		new(big.Int).Mul(cloneBigInt(g.config.CirculatingSupply), new(big.Int).SetUint64(constants.GovernanceQuorumBPS)),
		new(big.Int).SetUint64(constants.BasisPoints),
	)

	response := FinalizeResponse{
		ProposalID:    proposal.ID,
		CurrentBlock:  g.currentBlock,
		ArchitectCut:  "0",
		SlashedAmount: "0",
		BurnAmount:    "0",
	}

	var eventToBroadcast *coregov.GovernanceEvent
	err := g.mutateLocked(func() error {
		if proposal.ForVotes.Cmp(proposal.AgainstVotes) > 0 && participation.Cmp(quorum) >= 0 {
			proposal.Executed = true
			response.Status = string(coregov.ProposalStatusExecuted)

			event := coregov.GovernanceEvent{
				ProposalID:    proposal.ID,
				Sector:        proposal.Sector,
				MultiplierBPS: proposal.MultiplierBPS,
				BlockNumber:   g.currentBlock,
				TransactionID: governanceTransactionID(proposal.ID, g.currentBlock),
				EmittedAt:     time.Now().UTC(),
			}
			g.events = append(g.events, event)
			response.GovernanceEvent = &event
			eventToBroadcast = &event
			return nil
		}

		proposal.Rejected = true
		proposal.ArchitectCut = constants.ArchitectFee(proposal.Stake)
		proposal.BurnAmount = new(big.Int).Sub(cloneBigInt(proposal.Stake), proposal.ArchitectCut)
		response.Status = string(coregov.ProposalStatusRejected)
		response.ArchitectCut = cloneBigInt(proposal.ArchitectCut).String()
		response.SlashedAmount = cloneBigInt(proposal.Stake).String()
		response.BurnAmount = cloneBigInt(proposal.BurnAmount).String()
		return nil
	})
	if err != nil {
		return FinalizeResponse{}, err
	}
	if eventToBroadcast != nil {
		g.broadcastEvent(*eventToBroadcast)
	}
	return response, nil
}

func (g *GovernanceStore) EventsSince(fromBlock uint64) []coregov.GovernanceEvent {
	g.mu.RLock()
	defer g.mu.RUnlock()

	events := make([]coregov.GovernanceEvent, 0, len(g.events))
	for _, event := range g.events {
		if event.BlockNumber < fromBlock {
			continue
		}
		events = append(events, event)
	}

	return events
}

func (g *GovernanceStore) SubscribeGovernanceEvents(ctx context.Context, fromBlock uint64) (<-chan coregov.GovernanceEvent, <-chan error, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	events := make(chan coregov.GovernanceEvent, 32)
	errs := make(chan error, 1)

	g.mu.Lock()
	historical := make([]coregov.GovernanceEvent, 0, len(g.events))
	for _, event := range g.events {
		if event.BlockNumber >= fromBlock {
			historical = append(historical, event)
		}
	}
	g.nextSubscriber++
	subscriberID := g.nextSubscriber
	live := make(chan coregov.GovernanceEvent, 32)
	g.subscribers[subscriberID] = live
	g.mu.Unlock()

	go func() {
		defer close(events)
		defer close(errs)
		defer func() {
			g.mu.Lock()
			delete(g.subscribers, subscriberID)
			close(live)
			g.mu.Unlock()
		}()

		for _, event := range historical {
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case event := <-live:
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return events, errs, nil
}

func (g *GovernanceStore) summarize(proposal *proposalRecord, currentBlock uint64) coregov.ProposalSummary {
	return coregov.ProposalSummary{
		ID:              proposal.ID,
		Title:           proposal.Title,
		TargetComponent: proposal.TargetComponent,
		LogicExtension:  proposal.LogicExtension,
		Proposer:        proposal.Proposer,
		ProposerAlias:   proposal.ProposerAlias,
		Status:          g.statusOf(proposal, currentBlock),
		DeadlineBlock:   proposal.EndBlock,
		ForVotes:        cloneBigInt(proposal.ForVotes).String(),
		AgainstVotes:    cloneBigInt(proposal.AgainstVotes).String(),
		AbstainVotes:    cloneBigInt(proposal.AbstainVotes).String(),
	}
}

func (g *GovernanceStore) statusOf(proposal *proposalRecord, currentBlock uint64) coregov.ProposalStatus {
	switch {
	case proposal.Executed:
		return coregov.ProposalStatusExecuted
	case proposal.Rejected:
		return coregov.ProposalStatusRejected
	case currentBlock <= proposal.EndBlock:
		return coregov.ProposalStatusActive
	default:
		return coregov.ProposalStatusQueued
	}
}

func (g *GovernanceStore) broadcastEvent(event coregov.GovernanceEvent) {
	for _, subscriber := range g.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func governanceTransactionID(proposalID, blockNumber uint64) string {
	sum := sha256.Sum256([]byte(strconv.FormatUint(proposalID, 10) + ":" + strconv.FormatUint(blockNumber, 10)))
	return hex.EncodeToString(sum[:16])
}

func parseBigInt(value string) (*big.Int, error) {
	clean := strings.TrimSpace(value)
	if clean == "" {
		return big.NewInt(0), nil
	}

	parsed, ok := new(big.Int).SetString(clean, 10)
	if !ok {
		return nil, errors.New("invalid integer")
	}

	return parsed, nil
}

func cloneBigInt(value *big.Int) *big.Int {
	if value == nil {
		return big.NewInt(0)
	}

	return new(big.Int).Set(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}

func writeError(writer http.ResponseWriter, statusCode int, message string) {
	writeJSON(writer, statusCode, map[string]string{"error": message})
}

func writeJSON(writer http.ResponseWriter, statusCode int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(payload)
}
