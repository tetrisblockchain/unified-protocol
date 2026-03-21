package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

const (
	BlocksTopic                 = "ufi-blocks"
	TransactionsTopic           = "ufi-transactions"
	ChainSyncProtocol           = protocol.ID("/ufi/chain-sync/1.0.0")
	MaxGossipBlockBytes         = 4 << 20
	MaxGossipTransactionBytes   = 1 << 20
	MaxChainSyncBatchSize       = 64
	MaxChainSyncRequestBytes    = 4 << 10
	MaxChainSyncResponseBytes   = 6 << 20
	DefaultPeerBlockLimit       = 48
	DefaultPeerTransactionLimit = 96
	DefaultPeerSyncLimit        = 24
	DefaultPeerLimitWindow      = time.Minute
	DefaultP2PStreamTimeout     = 10 * time.Second
)

type ChainSyncProvider interface {
	LatestBlock() Block
	GetBlockByNumber(number uint64) (Block, error)
}

type P2PConfig struct {
	ListenAddrs     []string
	Bootnodes       []string
	IdentityKeyPath string
}

type P2PNode struct {
	host          host.Host
	pubsub        *pubsub.PubSub
	topic         *pubsub.Topic
	sub           *pubsub.Subscription
	txTopic       *pubsub.Topic
	txSub         *pubsub.Subscription
	logger        *log.Logger
	chainProvider ChainSyncProvider
	onBlock       func(Block) error
	onTransaction func(Transaction) error
	blockLimiter  *peerWindowLimiter
	txLimiter     *peerWindowLimiter
	syncLimiter   *peerWindowLimiter
	reputation    *PeerReputationBook
}

type chainSyncRequest struct {
	StartNumber uint64 `json:"startNumber"`
	Limit       int    `json:"limit"`
}

type chainSyncResponse struct {
	LatestNumber uint64  `json:"latestNumber"`
	Blocks       []Block `json:"blocks,omitempty"`
	Error        string  `json:"error,omitempty"`
}

func NewP2PNode(ctx context.Context, config P2PConfig, logger *log.Logger, chainProvider ChainSyncProvider, onBlock func(Block) error) (*P2PNode, error) {
	if logger == nil {
		logger = log.Default()
	}
	if len(config.ListenAddrs) == 0 {
		config.ListenAddrs = []string{"/ip4/0.0.0.0/tcp/0"}
	}

	options := []libp2p.Option{libp2p.ListenAddrStrings(config.ListenAddrs...)}
	if strings.TrimSpace(config.IdentityKeyPath) != "" {
		privateKey, err := loadOrCreateP2PIdentity(config.IdentityKeyPath)
		if err != nil {
			return nil, err
		}
		options = append(options, libp2p.Identity(privateKey))
	}

	host, err := libp2p.New(options...)
	if err != nil {
		return nil, err
	}
	ps, err := pubsub.NewGossipSub(ctx, host)
	if err != nil {
		_ = host.Close()
		return nil, err
	}
	topic, err := ps.Join(BlocksTopic)
	if err != nil {
		_ = host.Close()
		return nil, err
	}
	sub, err := topic.Subscribe()
	if err != nil {
		_ = topic.Close()
		_ = host.Close()
		return nil, err
	}
	txTopic, err := ps.Join(TransactionsTopic)
	if err != nil {
		sub.Cancel()
		_ = topic.Close()
		_ = host.Close()
		return nil, err
	}
	txSub, err := txTopic.Subscribe()
	if err != nil {
		_ = txTopic.Close()
		sub.Cancel()
		_ = topic.Close()
		_ = host.Close()
		return nil, err
	}

	node := &P2PNode{
		host:          host,
		pubsub:        ps,
		topic:         topic,
		sub:           sub,
		txTopic:       txTopic,
		txSub:         txSub,
		logger:        logger,
		chainProvider: chainProvider,
		onBlock:       onBlock,
		blockLimiter:  newPeerWindowLimiter(DefaultPeerBlockLimit, DefaultPeerLimitWindow),
		txLimiter:     newPeerWindowLimiter(DefaultPeerTransactionLimit, DefaultPeerLimitWindow),
		syncLimiter:   newPeerWindowLimiter(DefaultPeerSyncLimit, DefaultPeerLimitWindow),
		reputation:    NewPeerReputationBook(),
	}
	host.SetStreamHandler(ChainSyncProtocol, node.handleChainSync)

	for _, bootnode := range config.Bootnodes {
		if err := node.connectBootnode(ctx, bootnode); err != nil {
			logger.Printf("bootnode connect failed for %s: %v", bootnode, err)
		}
	}
	return node, nil
}

func loadOrCreateP2PIdentity(path string) (crypto.PrivKey, error) {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return nil, errors.New("empty p2p identity path")
	}
	if data, err := os.ReadFile(cleaned); err == nil {
		return crypto.UnmarshalPrivateKey(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(cleaned), 0o755); err != nil {
		return nil, err
	}
	privateKey, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, err
	}
	encoded, err := crypto.MarshalPrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(cleaned, encoded, 0o600); err != nil {
		return nil, err
	}
	return privateKey, nil
}

func (p *P2PNode) Start(ctx context.Context) {
	if p == nil {
		return
	}
	go p.consumeBlocks(ctx)
	go p.consumeTransactions(ctx)
}

func (p *P2PNode) consumeBlocks(ctx context.Context) {
	for {
		message, err := p.sub.Next(ctx)
		if err != nil {
			return
		}
		if message.ReceivedFrom == p.host.ID() {
			continue
		}
		now := time.Now()
		if p.reputation != nil && !p.reputation.Allowed(message.ReceivedFrom, now) {
			p.logger.Printf("dropping block gossip from banned peer %s", message.ReceivedFrom)
			p.disconnectPeer(message.ReceivedFrom)
			continue
		}
		if len(message.Data) > MaxGossipBlockBytes {
			p.penalizePeer(message.ReceivedFrom, 20, "oversized block gossip")
			p.logger.Printf("discarding oversized block gossip from %s", message.ReceivedFrom)
			continue
		}
		blockLimit := DefaultPeerBlockLimit
		if p.reputation != nil {
			blockLimit = p.reputation.AdaptiveLimit(message.ReceivedFrom, DefaultPeerBlockLimit)
		}
		if blockLimit == 0 {
			p.disconnectPeer(message.ReceivedFrom)
			continue
		}
		if p.blockLimiter != nil && !p.blockLimiter.AllowWithLimit(message.ReceivedFrom, now, blockLimit) {
			p.penalizePeer(message.ReceivedFrom, 4, "block gossip rate limit exceeded")
			p.logger.Printf("rate limiting block gossip from %s", message.ReceivedFrom)
			continue
		}

		var block Block
		if err := json.Unmarshal(message.Data, &block); err != nil {
			p.penalizePeer(message.ReceivedFrom, 25, "invalid block gossip payload")
			p.logger.Printf("discarding invalid block payload: %v", err)
			continue
		}
		if p.onBlock != nil {
			if err := p.onBlock(block); err != nil {
				penalty := 10
				if !errors.Is(err, ErrForkNotPreferred) {
					penalty = 30
				}
				p.penalizePeer(message.ReceivedFrom, penalty, err.Error())
				p.logger.Printf("block import failed: %v", err)
				continue
			}
		}
		p.rewardPeer(message.ReceivedFrom, 2, "accepted block gossip")
	}
}

func (p *P2PNode) consumeTransactions(ctx context.Context) {
	for {
		if p == nil || p.txSub == nil {
			return
		}
		message, err := p.txSub.Next(ctx)
		if err != nil {
			return
		}
		if message.ReceivedFrom == p.host.ID() {
			continue
		}
		now := time.Now()
		if p.reputation != nil && !p.reputation.Allowed(message.ReceivedFrom, now) {
			p.logger.Printf("dropping transaction gossip from banned peer %s", message.ReceivedFrom)
			p.disconnectPeer(message.ReceivedFrom)
			continue
		}
		if len(message.Data) > MaxGossipTransactionBytes {
			p.penalizePeer(message.ReceivedFrom, 10, "oversized transaction gossip")
			p.logger.Printf("discarding oversized transaction gossip from %s", message.ReceivedFrom)
			continue
		}
		txLimit := DefaultPeerTransactionLimit
		if p.reputation != nil {
			txLimit = p.reputation.AdaptiveLimit(message.ReceivedFrom, DefaultPeerTransactionLimit)
		}
		if txLimit == 0 {
			p.disconnectPeer(message.ReceivedFrom)
			continue
		}
		if p.txLimiter != nil && !p.txLimiter.AllowWithLimit(message.ReceivedFrom, now, txLimit) {
			p.penalizePeer(message.ReceivedFrom, 2, "transaction gossip rate limit exceeded")
			p.logger.Printf("rate limiting transaction gossip from %s", message.ReceivedFrom)
			continue
		}

		var tx Transaction
		if err := json.Unmarshal(message.Data, &tx); err != nil {
			p.penalizePeer(message.ReceivedFrom, 15, "invalid transaction gossip payload")
			p.logger.Printf("discarding invalid transaction payload: %v", err)
			continue
		}
		if p.onTransaction != nil {
			if err := p.onTransaction(tx); err != nil {
				p.logger.Printf("transaction import failed from %s: %v", message.ReceivedFrom, err)
				continue
			}
		}
		p.rewardPeer(message.ReceivedFrom, 1, "accepted transaction gossip")
	}
}

func (p *P2PNode) PublishBlock(ctx context.Context, block Block) error {
	if p == nil || p.topic == nil {
		return nil
	}
	payload, err := json.Marshal(block)
	if err != nil {
		return err
	}
	return p.topic.Publish(ctx, payload)
}

func (p *P2PNode) PublishTransaction(ctx context.Context, tx Transaction) error {
	if p == nil || p.txTopic == nil {
		return nil
	}
	payload, err := json.Marshal(tx)
	if err != nil {
		return err
	}
	return p.txTopic.Publish(ctx, payload)
}

func (p *P2PNode) SetTransactionHandler(handler func(Transaction) error) {
	if p == nil {
		return
	}
	p.onTransaction = handler
}

func (p *P2PNode) SyncChain(ctx context.Context, localHeight uint64, importBlock func(Block) error) error {
	if p == nil || p.host == nil || importBlock == nil {
		return nil
	}

	peers := append([]peer.ID(nil), p.host.Network().Peers()...)
	sort.Slice(peers, func(i, j int) bool {
		left := peers[i]
		right := peers[j]
		leftScore := DefaultPeerScore
		rightScore := DefaultPeerScore
		if p.reputation != nil {
			leftScore = p.reputation.Score(left)
			rightScore = p.reputation.Score(right)
		}
		if leftScore == rightScore {
			return left.String() < right.String()
		}
		return leftScore > rightScore
	})
	if len(peers) == 0 {
		return nil
	}

	var lastErr error
	height := localHeight
	for _, peerID := range peers {
		if _, err := p.syncFromPeer(ctx, peerID, &height, importBlock); err != nil {
			lastErr = err
			p.logger.Printf("chain sync from peer %s failed: %v", peerID, err)
			continue
		}
	}
	return lastErr
}

func (p *P2PNode) Addresses() []string {
	if p == nil || p.host == nil {
		return nil
	}
	addresses := make([]string, 0, len(p.host.Addrs()))
	for _, addr := range p.host.Addrs() {
		addresses = append(addresses, addr.Encapsulate(ma.StringCast("/p2p/"+p.host.ID().String())).String())
	}
	sortStrings(addresses)
	return addresses
}

func (p *P2PNode) Close() error {
	if p == nil {
		return nil
	}
	if p.sub != nil {
		p.sub.Cancel()
	}
	if p.topic != nil {
		_ = p.topic.Close()
	}
	if p.txSub != nil {
		p.txSub.Cancel()
	}
	if p.txTopic != nil {
		_ = p.txTopic.Close()
	}
	if p.host != nil {
		return p.host.Close()
	}
	return nil
}

func (p *P2PNode) connectBootnode(ctx context.Context, address string) error {
	cleaned := strings.TrimSpace(address)
	if cleaned == "" {
		return nil
	}
	multiaddr, err := ma.NewMultiaddr(cleaned)
	if err != nil {
		return err
	}
	info, err := peer.AddrInfoFromP2pAddr(multiaddr)
	if err != nil {
		return err
	}
	return p.host.Connect(ctx, *info)
}

func (p *P2PNode) syncFromPeer(ctx context.Context, peerID peer.ID, localHeight *uint64, importBlock func(Block) error) (int, error) {
	if p.reputation != nil && !p.reputation.Allowed(peerID, time.Now()) {
		return 0, errors.New("peer temporarily banned")
	}

	next := *localHeight + 1
	imported := 0
	for {
		response, err := p.requestBlocks(ctx, peerID, next, DefaultSyncBatchSize)
		if err != nil {
			p.penalizePeer(peerID, 8, err.Error())
			return imported, err
		}
		if response.Error != "" {
			p.penalizePeer(peerID, 6, response.Error)
			return imported, errors.New(response.Error)
		}
		if response.LatestNumber < next || len(response.Blocks) == 0 {
			p.rewardPeer(peerID, 1, "chain sync completed")
			return imported, nil
		}
		if err := validateSyncResponse(next, response); err != nil {
			p.penalizePeer(peerID, 20, err.Error())
			return imported, err
		}

		for _, block := range response.Blocks {
			if err := importBlock(block); err != nil {
				p.penalizePeer(peerID, 12, err.Error())
				return imported, err
			}
			*localHeight = block.Header.Number
			next = block.Header.Number + 1
			imported++
		}
		if next > response.LatestNumber {
			p.rewardPeer(peerID, minInt(4, imported), "chain sync imported blocks")
			return imported, nil
		}
	}
}

func (p *P2PNode) requestBlocks(ctx context.Context, peerID peer.ID, startNumber uint64, limit int) (chainSyncResponse, error) {
	stream, err := p.host.NewStream(ctx, peerID, ChainSyncProtocol)
	if err != nil {
		return chainSyncResponse{}, err
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(DefaultP2PStreamTimeout))

	request := chainSyncRequest{
		StartNumber: startNumber,
		Limit:       limit,
	}
	if request.Limit <= 0 {
		request.Limit = DefaultSyncBatchSize
	}
	if request.Limit > MaxChainSyncBatchSize {
		request.Limit = MaxChainSyncBatchSize
	}

	if err := json.NewEncoder(stream).Encode(request); err != nil {
		return chainSyncResponse{}, err
	}

	var response chainSyncResponse
	if err := json.NewDecoder(io.LimitReader(stream, MaxChainSyncResponseBytes)).Decode(&response); err != nil {
		return chainSyncResponse{}, err
	}
	return response, nil
}

func (p *P2PNode) handleChainSync(stream network.Stream) {
	defer stream.Close()
	if p.chainProvider == nil {
		_ = json.NewEncoder(stream).Encode(chainSyncResponse{Error: "chain sync unavailable"})
		return
	}
	_ = stream.SetDeadline(time.Now().Add(DefaultP2PStreamTimeout))
	remotePeer := stream.Conn().RemotePeer()
	now := time.Now()
	if p.reputation != nil && !p.reputation.Allowed(remotePeer, now) {
		_ = json.NewEncoder(stream).Encode(chainSyncResponse{Error: "peer temporarily banned"})
		p.disconnectPeer(remotePeer)
		return
	}
	syncLimit := DefaultPeerSyncLimit
	if p.reputation != nil {
		syncLimit = p.reputation.AdaptiveLimit(remotePeer, DefaultPeerSyncLimit)
	}
	if syncLimit == 0 {
		_ = json.NewEncoder(stream).Encode(chainSyncResponse{Error: "peer temporarily banned"})
		p.disconnectPeer(remotePeer)
		return
	}
	if p.syncLimiter != nil && !p.syncLimiter.AllowWithLimit(remotePeer, now, syncLimit) {
		p.penalizePeer(remotePeer, 4, "chain sync rate limit exceeded")
		_ = json.NewEncoder(stream).Encode(chainSyncResponse{Error: "rate limited"})
		return
	}

	var request chainSyncRequest
	if err := json.NewDecoder(io.LimitReader(stream, MaxChainSyncRequestBytes)).Decode(&request); err != nil {
		p.penalizePeer(remotePeer, 10, "invalid chain sync request")
		_ = json.NewEncoder(stream).Encode(chainSyncResponse{Error: err.Error()})
		return
	}
	if request.Limit <= 0 {
		request.Limit = DefaultSyncBatchSize
	}
	maxLimit := MaxChainSyncBatchSize
	if syncLimit < maxLimit {
		maxLimit = syncLimit
	}
	if request.Limit > maxLimit {
		request.Limit = maxLimit
	}

	latest := p.chainProvider.LatestBlock()
	response := chainSyncResponse{
		LatestNumber: latest.Header.Number,
	}
	if request.StartNumber > latest.Header.Number {
		_ = json.NewEncoder(stream).Encode(response)
		return
	}

	response.Blocks = make([]Block, 0, request.Limit)
	for number := request.StartNumber; number <= latest.Header.Number && len(response.Blocks) < request.Limit; number++ {
		block, err := p.chainProvider.GetBlockByNumber(number)
		if err != nil {
			p.penalizePeer(remotePeer, 6, err.Error())
			response.Error = err.Error()
			break
		}
		response.Blocks = append(response.Blocks, block)
	}
	if response.Error == "" {
		p.rewardPeer(remotePeer, 1, "served chain sync request")
	}
	_ = json.NewEncoder(stream).Encode(response)
}

func sortStrings(values []string) {
	if len(values) < 2 {
		return
	}
	sort.Strings(values)
}

type peerWindowLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[peer.ID]peerWindow
}

type peerWindow struct {
	start time.Time
	count int
}

func newPeerWindowLimiter(limit int, window time.Duration) *peerWindowLimiter {
	return &peerWindowLimiter{
		limit:   limit,
		window:  window,
		entries: make(map[peer.ID]peerWindow),
	}
}

func (l *peerWindowLimiter) Allow(id peer.ID, now time.Time) bool {
	return l.AllowWithLimit(id, now, 0)
}

func (l *peerWindowLimiter) AllowWithLimit(id peer.ID, now time.Time, limit int) bool {
	if l == nil || l.window <= 0 {
		return true
	}
	if id == "" {
		return false
	}
	effectiveLimit := l.limit
	if limit > 0 && (effectiveLimit <= 0 || limit < effectiveLimit) {
		effectiveLimit = limit
	}
	if effectiveLimit <= 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[id]
	if !ok || now.Sub(entry.start) >= l.window {
		l.entries[id] = peerWindow{start: now, count: 1}
		l.pruneLocked(now)
		return true
	}
	if entry.count >= effectiveLimit {
		return false
	}
	entry.count++
	l.entries[id] = entry
	return true
}

func (l *peerWindowLimiter) pruneLocked(now time.Time) {
	for id, entry := range l.entries {
		if now.Sub(entry.start) >= l.window {
			delete(l.entries, id)
		}
	}
}

func (p *P2PNode) disconnectPeer(id peer.ID) {
	if p == nil || p.host == nil || id == "" {
		return
	}
	_ = p.host.Network().ClosePeer(id)
}

func (p *P2PNode) penalizePeer(id peer.ID, delta int, reason string) {
	if p == nil || p.reputation == nil || id == "" {
		return
	}
	status := p.reputation.Penalize(id, delta, reason)
	if status.Banned {
		p.logger.Printf("banning peer %s until %s: %s", id, status.BannedUntil.UTC().Format(time.RFC3339), reason)
		p.disconnectPeer(id)
	}
}

func (p *P2PNode) rewardPeer(id peer.ID, delta int, reason string) {
	if p == nil || p.reputation == nil || id == "" {
		return
	}
	_ = p.reputation.Reward(id, delta, reason)
}

func (p *P2PNode) PeerStatuses() []PeerReputationStatus {
	if p == nil || p.reputation == nil {
		return nil
	}
	return p.reputation.Snapshot(p.connectedPeerSet())
}

func (p *P2PNode) PeerSummary() PeerReputationSummary {
	if p == nil || p.reputation == nil {
		return PeerReputationSummary{}
	}
	return p.reputation.Summary(p.connectedPeerSet())
}

func (p *P2PNode) connectedPeerSet() map[peer.ID]bool {
	connected := make(map[peer.ID]bool)
	if p == nil || p.host == nil {
		return connected
	}
	for _, id := range p.host.Network().Peers() {
		connected[id] = true
	}
	return connected
}

func validateSyncResponse(expectedStart uint64, response chainSyncResponse) error {
	if len(response.Blocks) > MaxChainSyncBatchSize {
		return errors.New("chain sync response exceeded batch limit")
	}
	next := expectedStart
	for _, block := range response.Blocks {
		if block.Header.Number != next {
			return errors.New("chain sync response block numbers are not contiguous")
		}
		next++
	}
	return nil
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
