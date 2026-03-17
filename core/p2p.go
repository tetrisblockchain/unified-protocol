package core

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

const (
	BlocksTopic       = "ufi-blocks"
	ChainSyncProtocol = protocol.ID("/ufi/chain-sync/1.0.0")
)

type ChainSyncProvider interface {
	LatestBlock() Block
	GetBlockByNumber(number uint64) (Block, error)
}

type P2PConfig struct {
	ListenAddrs []string
	Bootnodes   []string
}

type P2PNode struct {
	host          host.Host
	pubsub        *pubsub.PubSub
	topic         *pubsub.Topic
	sub           *pubsub.Subscription
	logger        *log.Logger
	chainProvider ChainSyncProvider
	onBlock       func(Block) error
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

	host, err := libp2p.New(libp2p.ListenAddrStrings(config.ListenAddrs...))
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

	node := &P2PNode{
		host:          host,
		pubsub:        ps,
		topic:         topic,
		sub:           sub,
		logger:        logger,
		chainProvider: chainProvider,
		onBlock:       onBlock,
	}
	host.SetStreamHandler(ChainSyncProtocol, node.handleChainSync)

	for _, bootnode := range config.Bootnodes {
		if err := node.connectBootnode(ctx, bootnode); err != nil {
			logger.Printf("bootnode connect failed for %s: %v", bootnode, err)
		}
	}
	return node, nil
}

func (p *P2PNode) Start(ctx context.Context) {
	if p == nil {
		return
	}
	go func() {
		for {
			message, err := p.sub.Next(ctx)
			if err != nil {
				return
			}
			if message.ReceivedFrom == p.host.ID() {
				continue
			}

			var block Block
			if err := json.Unmarshal(message.Data, &block); err != nil {
				p.logger.Printf("discarding invalid block payload: %v", err)
				continue
			}
			if p.onBlock != nil {
				if err := p.onBlock(block); err != nil {
					p.logger.Printf("block import failed: %v", err)
				}
			}
		}
	}()
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

func (p *P2PNode) SyncChain(ctx context.Context, localHeight uint64, importBlock func(Block) error) error {
	if p == nil || p.host == nil || importBlock == nil {
		return nil
	}

	peers := append([]peer.ID(nil), p.host.Network().Peers()...)
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].String() < peers[j].String()
	})
	if len(peers) == 0 {
		return nil
	}

	var lastErr error
	height := localHeight
	for _, peerID := range peers {
		if err := p.syncFromPeer(ctx, peerID, &height, importBlock); err != nil {
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

func (p *P2PNode) syncFromPeer(ctx context.Context, peerID peer.ID, localHeight *uint64, importBlock func(Block) error) error {
	next := *localHeight + 1
	for {
		response, err := p.requestBlocks(ctx, peerID, next, DefaultSyncBatchSize)
		if err != nil {
			return err
		}
		if response.Error != "" {
			return errors.New(response.Error)
		}
		if response.LatestNumber < next || len(response.Blocks) == 0 {
			return nil
		}

		for _, block := range response.Blocks {
			if block.Header.Number < next {
				continue
			}
			if err := importBlock(block); err != nil {
				return err
			}
			*localHeight = block.Header.Number
			next = block.Header.Number + 1
		}
		if next > response.LatestNumber {
			return nil
		}
	}
}

func (p *P2PNode) requestBlocks(ctx context.Context, peerID peer.ID, startNumber uint64, limit int) (chainSyncResponse, error) {
	stream, err := p.host.NewStream(ctx, peerID, ChainSyncProtocol)
	if err != nil {
		return chainSyncResponse{}, err
	}
	defer stream.Close()

	request := chainSyncRequest{
		StartNumber: startNumber,
		Limit:       limit,
	}
	if request.Limit <= 0 {
		request.Limit = DefaultSyncBatchSize
	}

	if err := json.NewEncoder(stream).Encode(request); err != nil {
		return chainSyncResponse{}, err
	}

	var response chainSyncResponse
	if err := json.NewDecoder(stream).Decode(&response); err != nil {
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

	var request chainSyncRequest
	if err := json.NewDecoder(stream).Decode(&request); err != nil {
		_ = json.NewEncoder(stream).Encode(chainSyncResponse{Error: err.Error()})
		return
	}
	if request.Limit <= 0 {
		request.Limit = DefaultSyncBatchSize
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
			response.Error = err.Error()
			break
		}
		response.Blocks = append(response.Blocks, block)
	}
	_ = json.NewEncoder(stream).Encode(response)
}

func sortStrings(values []string) {
	if len(values) < 2 {
		return
	}
	sort.Strings(values)
}
