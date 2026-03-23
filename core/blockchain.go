package core

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	badger "github.com/dgraph-io/badger/v4"

	"unified/core/consensus"
	"unified/core/constants"
	"unified/core/types"
)

const (
	SearchEscrowAddress = "UFI_SEARCH_ESCROW"

	searchTaskTxWorkUnits                = uint64(2)
	maxProofWorkBodyBytes                = 32 << 10
	maxProofWorkUniqueTerms              = 256
	proofWorkTermBoostBPS                = uint64(15)
	proofWorkBodyChunkBytes              = 128
	maxProofWorkBodyBoostBPS             = uint64(2500)
	maxProofWorkHostPenaltyFloorBPS      = uint64(2500)
	maxProofWorkSubmitterPenaltyFloorBPS = uint64(4000)

	blockNumberPrefix    = "blocks/number/"
	blockHashPrefix      = "blocks/hash/"
	blockMetaPrefix      = "blocks/meta/"
	txPrefix             = "tx/"
	balancePrefix        = "state/balance/"
	noncePrefix          = "state/nonce/"
	searchPrefix         = "state/search/"
	mentionPrefix        = "state/mention/"
	taskPrefix           = "state/task/"
	namePrefix           = "state/name/"
	stateHistoryPrefix   = "state/history/"
	metaNetworkConfigKey = "meta/network_config"
	metaLatestHashKey    = "meta/latest_hash"
	metaLatestBlockKey   = "meta/latest_number"

	autonomousCrawlMaxDepth         = uint64(2)
	autonomousCrawlLinksPerProof    = 4
	autonomousCrawlPendingTaskLimit = 1024
	storageCompressionThreshold     = 4 << 10
)

var (
	ErrBlockNotFound         = errors.New("core: block not found")
	ErrCrawlProofNotFound    = errors.New("core: crawl proof not found")
	ErrInsufficientBalance   = errors.New("core: insufficient balance")
	ErrInvalidBlock          = errors.New("core: invalid block")
	ErrInvalidSignature      = errors.New("core: invalid transaction signature")
	ErrInvalidTransaction    = errors.New("core: invalid transaction")
	ErrInvalidTransactionFee = errors.New("core: architect fee mismatch")
	ErrInvalidNonce          = errors.New("core: invalid nonce")
	ErrTaskNotFound          = errors.New("core: task not found")
	ErrTaskSettled           = errors.New("core: task already settled")
	ErrTransactionNotFound   = errors.New("core: transaction not found")
)

type TxType string

const (
	TxTypeTransfer   TxType = "transfer"
	TxTypeSearchTask TxType = "search_task"
)

type SearchTaskRequest struct {
	Query           string `json:"query"`
	URL             string `json:"url"`
	BaseBounty      string `json:"baseBounty"`
	Difficulty      uint64 `json:"difficulty"`
	DataVolumeBytes uint64 `json:"dataVolumeBytes"`
}

type SearchTaskRecord struct {
	ID              string    `json:"id"`
	TxHash          string    `json:"txHash"`
	Submitter       string    `json:"submitter"`
	Query           string    `json:"query"`
	URL             string    `json:"url"`
	BaseBounty      string    `json:"baseBounty"`
	GrossBounty     string    `json:"grossBounty"`
	ArchitectFee    string    `json:"architectFee"`
	MinerReward     string    `json:"minerReward"`
	Difficulty      uint64    `json:"difficulty"`
	DataVolumeBytes uint64    `json:"dataVolumeBytes"`
	CreatedAt       time.Time `json:"createdAt"`
	Autonomous      bool      `json:"autonomous,omitempty"`
	ParentTaskID    string    `json:"parentTaskId,omitempty"`
	Depth           uint64    `json:"depth,omitempty"`
	Completed       bool      `json:"completed"`
	CompletedAt     time.Time `json:"completedAt,omitempty"`
	ProofHash       string    `json:"proofHash,omitempty"`
	MinedBy         string    `json:"minedBy,omitempty"`
}

type SearchRecord struct {
	TaskID      string            `json:"taskId"`
	URL         string            `json:"url"`
	Query       string            `json:"query"`
	Title       string            `json:"title"`
	Snippet     string            `json:"snippet"`
	Body        string            `json:"body"`
	ContentHash string            `json:"contentHash"`
	SimHash     uint64            `json:"simHash"`
	IndexedAt   time.Time         `json:"indexedAt"`
	IndexedBy   string            `json:"indexedBy"`
	ProofHash   string            `json:"proofHash"`
	BlockHash   string            `json:"blockHash"`
	TermCounts  map[string]uint64 `json:"termCounts"`
}

type SearchQueryResult struct {
	Document         *SearchRecord `json:"document,omitempty"`
	MentionFrequency uint64        `json:"mentionFrequency"`
	MatchingURLs     []string      `json:"matchingUrls,omitempty"`
}

type SearchHit struct {
	Document  SearchRecord `json:"document"`
	TermCount uint64       `json:"termCount"`
	Score     uint64       `json:"score"`
}

type SearchResults struct {
	Term             string      `json:"term"`
	URLFilter        string      `json:"urlFilter,omitempty"`
	MentionFrequency uint64      `json:"mentionFrequency"`
	Total            uint64      `json:"total"`
	Hits             []SearchHit `json:"hits,omitempty"`
}

type NameRecord struct {
	Name         string    `json:"name"`
	Owner        string    `json:"owner"`
	RegisteredAt time.Time `json:"registeredAt"`
	TxHash       string    `json:"txHash"`
}

type Transaction struct {
	Hash      string    `json:"hash,omitempty"`
	Type      TxType    `json:"type"`
	From      string    `json:"from"`
	To        string    `json:"to,omitempty"`
	Value     string    `json:"value"`
	Nonce     uint64    `json:"nonce"`
	Timestamp time.Time `json:"timestamp"`
	Data      []byte    `json:"data,omitempty"`
	PublicKey []byte    `json:"publicKey,omitempty"`
	Signature []byte    `json:"signature,omitempty"`
}

type CrawlProof struct {
	TaskID       string                `json:"taskId"`
	TaskTxHash   string                `json:"taskTxHash"`
	Query        string                `json:"query"`
	URL          string                `json:"url"`
	Miner        string                `json:"miner"`
	Page         consensus.IndexedPage `json:"page"`
	ProofHash    string                `json:"proofHash"`
	GrossBounty  string                `json:"grossBounty"`
	ArchitectFee string                `json:"architectFee"`
	MinerReward  string                `json:"minerReward"`
	CreatedAt    time.Time             `json:"createdAt"`
}

type BlockHeader struct {
	Number     uint64    `json:"number"`
	ParentHash string    `json:"parentHash"`
	StateRoot  string    `json:"stateRoot"`
	Timestamp  time.Time `json:"timestamp"`
	Miner      string    `json:"miner"`
	TxRoot     string    `json:"txRoot"`
	CrawlRoot  string    `json:"crawlRoot"`
}

type BlockBody struct {
	Transactions []Transaction `json:"transactions"`
	CrawlProofs  []CrawlProof  `json:"crawlProofs"`
}

type Block struct {
	Hash   string      `json:"hash"`
	Header BlockHeader `json:"header"`
	Body   BlockBody   `json:"body"`
}

type ActivityTransaction struct {
	BlockNumber uint64      `json:"blockNumber"`
	BlockHash   string      `json:"blockHash"`
	Transaction Transaction `json:"transaction"`
}

type ActivityProof struct {
	BlockNumber uint64     `json:"blockNumber"`
	BlockHash   string     `json:"blockHash"`
	Proof       CrawlProof `json:"proof"`
}

type RecentActivity struct {
	LatestBlock  uint64                `json:"latestBlock"`
	Transactions []ActivityTransaction `json:"transactions,omitempty"`
	Proofs       []ActivityProof       `json:"proofs,omitempty"`
}

type AddressActivity struct {
	Address      string                `json:"address"`
	Alias        string                `json:"alias,omitempty"`
	Balance      string                `json:"balance"`
	LatestNonce  uint64                `json:"latestNonce"`
	Transactions []ActivityTransaction `json:"transactions,omitempty"`
	Proofs       []ActivityProof       `json:"proofs,omitempty"`
}

type BlockchainConfig struct {
	DataDir         string
	Logger          *log.Logger
	GenesisBalances map[string]*big.Int
	Network         NetworkConfig
}

type Blockchain struct {
	mu         sync.RWMutex
	db         *badger.DB
	logger     *log.Logger
	state      *StateSnapshot
	latest     Block
	latestMeta BlockMeta
	network    NetworkConfig
	datadir    string
}

type StateSnapshot struct {
	Balances      map[string]*big.Int
	Nonces        map[string]uint64
	PendingTasks  map[string]SearchTaskRecord
	SearchIndex   map[string]SearchRecord
	MentionCounts map[string]uint64
	Names         map[string]NameRecord
}

type stateSnapshotDisk struct {
	Balances      map[string]string           `json:"balances"`
	Nonces        map[string]uint64           `json:"nonces"`
	PendingTasks  map[string]SearchTaskRecord `json:"pendingTasks"`
	SearchIndex   map[string]SearchRecord     `json:"searchIndex"`
	MentionCounts map[string]uint64           `json:"mentionCounts"`
	Names         map[string]NameRecord       `json:"names"`
}

type BlockMeta struct {
	Hash           string `json:"hash"`
	ParentHash     string `json:"parentHash"`
	Number         uint64 `json:"number"`
	Work           string `json:"work"`
	CumulativeWork string `json:"cumulativeWork"`
	Canonical      bool   `json:"canonical"`
}

type AppliedTransaction struct {
	Transaction  Transaction
	ArchitectFee *big.Int
	NetValue     *big.Int
	Task         *SearchTaskRecord
}

type SearchTaskEnvelope struct {
	Transaction Transaction
	Request     SearchTaskRequest
	Task        consensus.CrawlTask
}

func NewStateSnapshot() *StateSnapshot {
	return &StateSnapshot{
		Balances:      make(map[string]*big.Int),
		Nonces:        make(map[string]uint64),
		PendingTasks:  make(map[string]SearchTaskRecord),
		SearchIndex:   make(map[string]SearchRecord),
		MentionCounts: make(map[string]uint64),
		Names:         make(map[string]NameRecord),
	}
}

func (s *StateSnapshot) Clone() *StateSnapshot {
	cloned := NewStateSnapshot()
	for address, balance := range s.Balances {
		cloned.Balances[address] = cloneBigInt(balance)
	}
	for address, nonce := range s.Nonces {
		cloned.Nonces[address] = nonce
	}
	for id, task := range s.PendingTasks {
		cloned.PendingTasks[id] = task
	}
	for url, record := range s.SearchIndex {
		copyRecord := record
		copyRecord.TermCounts = cloneUint64Map(record.TermCounts)
		cloned.SearchIndex[url] = copyRecord
	}
	for term, count := range s.MentionCounts {
		cloned.MentionCounts[term] = count
	}
	for name, record := range s.Names {
		cloned.Names[name] = record
	}
	return cloned
}

func (m BlockMeta) WorkBig() *big.Int {
	value, ok := new(big.Int).SetString(strings.TrimSpace(m.Work), 10)
	if !ok {
		return big.NewInt(0)
	}
	return value
}

func (m BlockMeta) CumulativeWorkBig() *big.Int {
	value, ok := new(big.Int).SetString(strings.TrimSpace(m.CumulativeWork), 10)
	if !ok {
		return big.NewInt(0)
	}
	return value
}

func OpenBlockchain(config BlockchainConfig) (*Blockchain, error) {
	if strings.TrimSpace(config.DataDir) == "" {
		return nil, fmt.Errorf("core: datadir is required")
	}
	if config.Logger == nil {
		config.Logger = log.Default()
	}
	network, err := NormalizeNetworkConfig(config.Network)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(config.DataDir, 0o755); err != nil {
		return nil, err
	}

	options := badger.DefaultOptions(config.DataDir).WithLogger(nil)
	db, err := badger.Open(options)
	if err != nil {
		return nil, err
	}

	chain := &Blockchain{
		db:      db,
		logger:  config.Logger,
		state:   NewStateSnapshot(),
		network: network,
		datadir: config.DataDir,
	}

	if err := chain.loadLocked(); err != nil {
		_ = db.Close()
		return nil, err
	}

	if chain.latest.Hash == "" {
		chain.network = network
		if strings.TrimSpace(chain.network.GenesisAddress) == "" {
			chain.network.GenesisAddress = inferGenesisAddress(config.GenesisBalances, chain.network.ArchitectAddress)
		}
		for address, balance := range config.GenesisBalances {
			chain.state.Balances[address] = cloneBigInt(balance)
		}
		genesis := Block{
			Header: BlockHeader{
				Number:     0,
				ParentHash: "",
				Timestamp:  time.Unix(constants.GenesisTimestampUnix, 0).UTC(),
				Miner:      chain.ArchitectAddress(),
			},
			Body: BlockBody{},
		}
		genesis.Header.TxRoot = merkleRoot(nil)
		genesis.Header.CrawlRoot = merkleRoot(nil)
		genesis.Header.StateRoot = computeStateRoot(chain.state)
		genesis.Hash = computeBlockHash(genesis)
		genesisMeta := BlockMeta{
			Hash:           genesis.Hash,
			ParentHash:     "",
			Number:         0,
			Work:           "0",
			CumulativeWork: "0",
			Canonical:      true,
		}
		if err := chain.persistCanonicalTipLocked(chain.state.Clone(), genesis, genesisMeta); err != nil {
			_ = db.Close()
			return nil, err
		}
		return chain, nil
	}

	resolved, changed, err := reconcileNetworkConfig(chain.network, network, chain.latest)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	chain.network = resolved
	if changed {
		if err := chain.persistStateOnlyLocked(); err != nil {
			_ = db.Close()
			return nil, err
		}
	}

	return chain, nil
}

func (bc *Blockchain) Close() error {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if bc.db == nil {
		return nil
	}
	if err := bc.db.Sync(); err != nil {
		return err
	}
	return bc.db.Close()
}

func (bc *Blockchain) LatestBlock() Block {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.latest
}

func (bc *Blockchain) NetworkConfig() NetworkConfig {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.network.Clone()
}

func (bc *Blockchain) ArchitectAddress() string {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return normalizedArchitectAddress(bc.network.ArchitectAddress)
}

func (bc *Blockchain) PendingNonce(address string) uint64 {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.state.Nonces[address]
}

func (bc *Blockchain) NonceAt(address, blockRef string) (uint64, error) {
	state, err := bc.stateForBlockRef(blockRef)
	if err != nil {
		return 0, err
	}
	return state.Nonces[strings.TrimSpace(address)], nil
}

func (bc *Blockchain) GetBalance(address string) *big.Int {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return cloneBigInt(bc.state.Balances[address])
}

func (bc *Blockchain) MentionFrequency(term string) uint64 {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.state.MentionCounts[normalizeTerm(term)]
}

func (bc *Blockchain) GetSearchData(url, term string) SearchQueryResult {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	result := SearchQueryResult{}
	normalizedTerm := normalizeTerm(term)
	if cleanedURL := strings.TrimSpace(url); cleanedURL != "" {
		if record, ok := bc.state.SearchIndex[cleanedURL]; ok {
			copyRecord := record
			copyRecord.TermCounts = cloneUint64Map(record.TermCounts)
			result.Document = &copyRecord
		}
	}
	if normalizedTerm != "" {
		result.MentionFrequency = bc.state.MentionCounts[normalizedTerm]
		for url, record := range bc.state.SearchIndex {
			if record.TermCounts[normalizedTerm] > 0 {
				result.MatchingURLs = append(result.MatchingURLs, url)
			}
		}
		sort.Strings(result.MatchingURLs)
	}
	return result
}

func (bc *Blockchain) SearchIndex(term, urlFilter string, limit uint64) SearchResults {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	result := SearchResults{
		Term:      strings.TrimSpace(term),
		URLFilter: strings.TrimSpace(urlFilter),
	}
	normalizedTerm := normalizeTerm(term)
	if normalizedTerm == "" {
		return result
	}

	result.MentionFrequency = bc.state.MentionCounts[normalizedTerm]
	filter := strings.ToLower(result.URLFilter)
	for url, record := range bc.state.SearchIndex {
		if filter != "" && !strings.Contains(strings.ToLower(url), filter) {
			continue
		}
		termCount := record.TermCounts[normalizedTerm]
		if termCount == 0 {
			continue
		}
		copyRecord := record
		copyRecord.TermCounts = cloneUint64Map(record.TermCounts)
		result.Hits = append(result.Hits, SearchHit{
			Document:  copyRecord,
			TermCount: termCount,
			Score:     termCount,
		})
	}

	sort.Slice(result.Hits, func(i, j int) bool {
		if result.Hits[i].Score != result.Hits[j].Score {
			return result.Hits[i].Score > result.Hits[j].Score
		}
		if !result.Hits[i].Document.IndexedAt.Equal(result.Hits[j].Document.IndexedAt) {
			return result.Hits[i].Document.IndexedAt.After(result.Hits[j].Document.IndexedAt)
		}
		return result.Hits[i].Document.URL < result.Hits[j].Document.URL
	})

	result.Total = uint64(len(result.Hits))
	maxItems := normalizeSearchResultLimit(limit)
	if len(result.Hits) > maxItems {
		result.Hits = append([]SearchHit(nil), result.Hits[:maxItems]...)
	}
	return result
}

func (bc *Blockchain) PendingSearchTaskRecords(limit int) []SearchTaskRecord {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	records := make([]SearchTaskRecord, 0, len(bc.state.PendingTasks))
	for _, task := range bc.state.PendingTasks {
		if task.Completed {
			continue
		}
		records = append(records, task)
	}
	sortPendingSearchTaskRecords(records)
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	return records
}

func (bc *Blockchain) PendingSearchTaskSummary() (pending int, autonomous int) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	for _, task := range bc.state.PendingTasks {
		if task.Completed {
			continue
		}
		pending++
		if task.Autonomous {
			autonomous++
		}
	}
	return pending, autonomous
}

func (bc *Blockchain) PrecompileMentionFrequency(term string) ([]byte, error) {
	input, err := EncodeMentionFrequencyCall(term)
	if err != nil {
		return nil, err
	}
	return bc.CallNativeContract(constants.SearchPrecompileAddress, input)
}

func (bc *Blockchain) CallNativeContract(to string, data []byte) ([]byte, error) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return CallNativeContract(bc.state, to, data)
}

func (bc *Blockchain) CallContract(call CallMessage, blockRef string) ([]byte, error) {
	state, err := bc.stateForBlockRef(blockRef)
	if err != nil {
		return nil, err
	}
	if !IsNativeContractAddress(call.To) {
		return nil, ErrUnsupportedNativeContract
	}
	return ExecuteReadOnlyCall(state, call)
}

func (bc *Blockchain) ContractAt(address string) (ContractRecord, bool) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	cleaned := strings.TrimSpace(address)
	for _, record := range bc.network.SystemContracts {
		if record.Address == cleaned {
			record.Functions = append([]ContractFunction(nil), record.Functions...)
			return record, true
		}
	}
	return SystemContractAt(address)
}

func (bc *Blockchain) ContractCodeAt(address string) string {
	record, ok := bc.ContractAt(address)
	if !ok {
		return "0x"
	}
	return record.Code
}

func (bc *Blockchain) ListContracts() []ContractRecord {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	if len(bc.network.SystemContracts) == 0 {
		return ListSystemContracts()
	}
	return cloneContractRecords(bc.network.SystemContracts)
}

func (bc *Blockchain) UNSRegistrationPrice(name string) (*big.Int, error) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return UNSRegistrationPriceFromState(bc.state, name)
}

func (bc *Blockchain) stateForBlockRef(blockRef string) (*StateSnapshot, error) {
	ref := strings.TrimSpace(strings.ToLower(blockRef))
	if ref == "" || ref == "latest" {
		bc.mu.RLock()
		defer bc.mu.RUnlock()
		return bc.state.Clone(), nil
	}

	number, err := parseBlockReference(ref, bc.LatestBlock().Header.Number)
	if err != nil {
		return nil, err
	}
	block, err := bc.GetBlockByNumber(number)
	if err != nil {
		return nil, err
	}

	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.getStateSnapshotLocked(block.Hash)
}

func (bc *Blockchain) ResolveName(name string) (NameRecord, bool) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	record, ok := bc.state.Names[normalizeUNSName(name)]
	return record, ok
}

func (bc *Blockchain) ReverseResolve(address string) (NameRecord, bool) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	target := strings.TrimSpace(address)
	if target == "" {
		return NameRecord{}, false
	}
	for _, record := range bc.state.Names {
		if record.Owner == target {
			return record, true
		}
	}
	return NameRecord{}, false
}

func (bc *Blockchain) SeedBalance(address string, amount *big.Int) error {
	if strings.TrimSpace(address) == "" || amount == nil || amount.Sign() < 0 {
		return ErrInvalidTransaction
	}

	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.state.Balances[address] = cloneBigInt(amount)
	return bc.persistStateOnlyLocked()
}

func (bc *Blockchain) ValidateTransaction(tx Transaction) error {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	snapshot := bc.state.Clone()
	_, err := ApplyTransactionWithArchitect(snapshot, tx, bc.network.ArchitectAddress)
	return err
}

func (bc *Blockchain) GetBlockByNumber(number uint64) (Block, error) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	if number == bc.latest.Header.Number {
		return bc.latest, nil
	}

	var block Block
	err := bc.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(blockNumberKey(number)))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return ErrBlockNotFound
			}
			return err
		}
		return item.Value(func(value []byte) error {
			return decodeStoredJSON(value, &block)
		})
	})
	return block, err
}

func (bc *Blockchain) GetBlockByHash(hash string) (Block, error) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	if hash == bc.latest.Hash {
		return bc.latest, nil
	}

	var block Block
	err := bc.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(blockHashKey(hash)))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return ErrBlockNotFound
			}
			return err
		}
		return item.Value(func(value []byte) error {
			return decodeStoredJSON(value, &block)
		})
	})
	return block, err
}

func (bc *Blockchain) RecentActivity(limit uint64) RecentActivity {
	maxItems := normalizeActivityLimit(limit)
	latest := bc.LatestBlock().Header.Number
	result := RecentActivity{LatestBlock: latest}

	for number := latest; ; number-- {
		block, err := bc.GetBlockByNumber(number)
		if err == nil {
			for idx := len(block.Body.Transactions) - 1; idx >= 0 && len(result.Transactions) < maxItems; idx-- {
				result.Transactions = append(result.Transactions, ActivityTransaction{
					BlockNumber: block.Header.Number,
					BlockHash:   block.Hash,
					Transaction: block.Body.Transactions[idx],
				})
			}
			for idx := len(block.Body.CrawlProofs) - 1; idx >= 0 && len(result.Proofs) < maxItems; idx-- {
				result.Proofs = append(result.Proofs, ActivityProof{
					BlockNumber: block.Header.Number,
					BlockHash:   block.Hash,
					Proof:       block.Body.CrawlProofs[idx],
				})
			}
		}
		if len(result.Transactions) >= maxItems && len(result.Proofs) >= maxItems {
			break
		}
		if number == 0 {
			break
		}
	}

	return result
}

func (bc *Blockchain) AddressActivity(address string, limit uint64) (AddressActivity, error) {
	cleaned := strings.TrimSpace(address)
	if cleaned == "" {
		return AddressActivity{}, ErrInvalidTransaction
	}

	maxItems := normalizeActivityLimit(limit)
	result := AddressActivity{
		Address:     cleaned,
		Balance:     bc.GetBalance(cleaned).String(),
		LatestNonce: bc.PendingNonce(cleaned),
	}
	if record, ok := bc.ReverseResolve(cleaned); ok {
		result.Alias = record.Name
	}

	latest := bc.LatestBlock().Header.Number
	relatedTaskHashes := make(map[string]struct{})
	for number := latest; ; number-- {
		block, err := bc.GetBlockByNumber(number)
		if err == nil {
			for idx := len(block.Body.Transactions) - 1; idx >= 0 && len(result.Transactions) < maxItems; idx-- {
				tx := block.Body.Transactions[idx]
				if tx.From != cleaned && tx.To != cleaned {
					continue
				}
				result.Transactions = append(result.Transactions, ActivityTransaction{
					BlockNumber: block.Header.Number,
					BlockHash:   block.Hash,
					Transaction: tx,
				})
				if tx.Type == TxTypeSearchTask {
					relatedTaskHashes[tx.Hash] = struct{}{}
				}
			}
		}
		if len(result.Transactions) >= maxItems || number == 0 {
			break
		}
	}

	for number := latest; ; number-- {
		block, err := bc.GetBlockByNumber(number)
		if err == nil {
			for idx := len(block.Body.CrawlProofs) - 1; idx >= 0 && len(result.Proofs) < maxItems; idx-- {
				proof := block.Body.CrawlProofs[idx]
				_, relatedTask := relatedTaskHashes[proof.TaskTxHash]
				if proof.Miner != cleaned && !relatedTask {
					continue
				}
				result.Proofs = append(result.Proofs, ActivityProof{
					BlockNumber: block.Header.Number,
					BlockHash:   block.Hash,
					Proof:       proof,
				})
			}
		}
		if len(result.Proofs) >= maxItems || number == 0 {
			break
		}
	}

	return result, nil
}

func (bc *Blockchain) TransactionByHash(hash string) (ActivityTransaction, error) {
	target := strings.TrimSpace(hash)
	if target == "" {
		return ActivityTransaction{}, ErrTransactionNotFound
	}

	latest := bc.LatestBlock().Header.Number
	for number := latest; ; number-- {
		block, err := bc.GetBlockByNumber(number)
		if err == nil {
			for _, tx := range block.Body.Transactions {
				if tx.Hash == target {
					return ActivityTransaction{
						BlockNumber: block.Header.Number,
						BlockHash:   block.Hash,
						Transaction: tx,
					}, nil
				}
			}
		}
		if number == 0 {
			break
		}
	}

	return ActivityTransaction{}, ErrTransactionNotFound
}

func (bc *Blockchain) CrawlProofByTask(taskTxHash, taskID string) (ActivityProof, error) {
	targetTx := strings.TrimSpace(taskTxHash)
	targetTask := strings.TrimSpace(taskID)
	if targetTx == "" && targetTask == "" {
		return ActivityProof{}, ErrCrawlProofNotFound
	}

	latest := bc.LatestBlock().Header.Number
	for number := latest; ; number-- {
		block, err := bc.GetBlockByNumber(number)
		if err == nil {
			for _, proof := range block.Body.CrawlProofs {
				if (targetTx != "" && proof.TaskTxHash == targetTx) || (targetTask != "" && proof.TaskID == targetTask) {
					return ActivityProof{
						BlockNumber: block.Header.Number,
						BlockHash:   block.Hash,
						Proof:       proof,
					}, nil
				}
			}
		}
		if number == 0 {
			break
		}
	}

	return ActivityProof{}, ErrCrawlProofNotFound
}

func (bc *Blockchain) HasBlockHash(hash string) bool {
	if strings.TrimSpace(hash) == "" {
		return false
	}
	_, err := bc.GetBlockByHash(hash)
	return err == nil
}

func normalizeActivityLimit(limit uint64) int {
	switch {
	case limit == 0:
		return 12
	case limit > 64:
		return 64
	default:
		return int(limit)
	}
}

func normalizeSearchResultLimit(limit uint64) int {
	switch {
	case limit == 0:
		return 10
	case limit > 50:
		return 50
	default:
		return int(limit)
	}
}

func (bc *Blockchain) ParentState(block Block) (*StateSnapshot, error) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.getStateSnapshotLocked(block.Header.ParentHash)
}

func (bc *Blockchain) getBlockByHashLocked(hash string) (Block, error) {
	if strings.TrimSpace(hash) == "" {
		return Block{}, ErrBlockNotFound
	}
	if hash == bc.latest.Hash {
		return bc.latest, nil
	}

	var block Block
	err := bc.db.View(func(txn *badger.Txn) error {
		loaded, err := loadBlockTxn(txn, hash)
		if err != nil {
			return err
		}
		block = loaded
		return nil
	})
	return block, err
}

func (bc *Blockchain) getBlockMetaLocked(hash string) (BlockMeta, error) {
	if strings.TrimSpace(hash) == "" {
		return BlockMeta{}, ErrBlockNotFound
	}
	if hash == bc.latestMeta.Hash {
		return bc.latestMeta, nil
	}

	var meta BlockMeta
	err := bc.db.View(func(txn *badger.Txn) error {
		loaded, err := loadBlockMetaTxn(txn, hash)
		if err != nil {
			return err
		}
		meta = loaded
		return nil
	})
	return meta, err
}

func (bc *Blockchain) getStateSnapshotLocked(hash string) (*StateSnapshot, error) {
	if strings.TrimSpace(hash) == "" {
		return nil, ErrBlockNotFound
	}
	if hash == bc.latest.Hash {
		return bc.state.Clone(), nil
	}

	var snapshot *StateSnapshot
	err := bc.db.View(func(txn *badger.Txn) error {
		loaded, err := loadStateSnapshotTxn(txn, hash)
		if err != nil {
			return err
		}
		snapshot = loaded
		return nil
	})
	return snapshot, err
}

func (bc *Blockchain) hasBlockMetaLocked(hash string) bool {
	if strings.TrimSpace(hash) == "" {
		return false
	}
	if hash == bc.latestMeta.Hash {
		return true
	}
	err := bc.db.View(func(txn *badger.Txn) error {
		_, err := loadBlockMetaTxn(txn, hash)
		return err
	})
	return err == nil
}

func (bc *Blockchain) MineBlock(miner string, transactions []Transaction, proofs []CrawlProof) (Block, error) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	parent := bc.latest
	block := Block{
		Header: BlockHeader{
			Number:     parent.Header.Number + 1,
			ParentHash: parent.Hash,
			Timestamp:  time.Now().UTC(),
			Miner:      miner,
		},
		Body: BlockBody{
			Transactions: append([]Transaction(nil), transactions...),
			CrawlProofs:  append([]CrawlProof(nil), proofs...),
		},
	}

	prepared, err := prepareBlock(parent, bc.state.Clone(), block, bc.network.ArchitectAddress)
	if err != nil {
		return Block{}, err
	}

	meta := BlockMeta{
		Hash:           prepared.Block.Hash,
		ParentHash:     prepared.Block.Header.ParentHash,
		Number:         prepared.Block.Header.Number,
		Work:           prepared.Work.String(),
		CumulativeWork: new(big.Int).Add(bc.latestMeta.CumulativeWorkBig(), prepared.Work).String(),
		Canonical:      true,
	}
	if err := bc.persistCanonicalTipLocked(prepared.State, prepared.Block, meta); err != nil {
		return Block{}, err
	}
	return prepared.Block, nil
}

func (bc *Blockchain) ImportBlock(block Block) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if block.Hash == "" {
		return ErrInvalidBlock
	}
	if bc.hasBlockMetaLocked(block.Hash) {
		return nil
	}

	parent, err := bc.getBlockByHashLocked(block.Header.ParentHash)
	if err != nil {
		return err
	}
	parentMeta, err := bc.getBlockMetaLocked(block.Header.ParentHash)
	if err != nil {
		return err
	}
	parentState, err := bc.getStateSnapshotLocked(block.Header.ParentHash)
	if err != nil {
		return err
	}

	prepared, err := prepareBlock(parent, parentState, block, bc.network.ArchitectAddress)
	if err != nil {
		return err
	}

	meta := BlockMeta{
		Hash:           prepared.Block.Hash,
		ParentHash:     prepared.Block.Header.ParentHash,
		Number:         prepared.Block.Header.Number,
		Work:           prepared.Work.String(),
		CumulativeWork: new(big.Int).Add(parentMeta.CumulativeWorkBig(), prepared.Work).String(),
	}
	better := isPreferredTip(meta, bc.latestMeta)
	meta.Canonical = better

	return bc.storeImportedBlockLocked(prepared.State, prepared.Block, meta, better)
}

type preparedBlock struct {
	Block Block
	State *StateSnapshot
	Work  *big.Int
}

func prepareBlock(parent Block, parentState *StateSnapshot, block Block, architectAddress string) (preparedBlock, error) {
	if parentState == nil {
		return preparedBlock{}, ErrInvalidBlock
	}
	if block.Header.Number != parent.Header.Number+1 {
		return preparedBlock{}, fmt.Errorf("%w: unexpected block number", ErrInvalidBlock)
	}
	if block.Header.ParentHash != parent.Hash {
		return preparedBlock{}, fmt.Errorf("%w: parent hash mismatch", ErrInvalidBlock)
	}

	snapshot := parentState.Clone()
	normalizedTxs := make([]Transaction, 0, len(block.Body.Transactions))
	for _, tx := range block.Body.Transactions {
		result, err := ApplyTransactionWithArchitect(snapshot, tx, architectAddress)
		if err != nil {
			return preparedBlock{}, err
		}
		normalizedTxs = append(normalizedTxs, result.Transaction)
	}

	work, err := computeBlockWork(snapshot, normalizedTxs, block.Body.CrawlProofs)
	if err != nil {
		return preparedBlock{}, err
	}

	normalizedProofs := make([]CrawlProof, 0, len(block.Body.CrawlProofs))
	for _, proof := range block.Body.CrawlProofs {
		normalized, err := ApplyCrawlProof(snapshot, proof, "")
		if err != nil {
			return preparedBlock{}, err
		}
		normalizedProofs = append(normalizedProofs, normalized)
	}

	expected := block
	expected.Body.Transactions = normalizedTxs
	expected.Body.CrawlProofs = normalizedProofs
	expected.Header.Number = parent.Header.Number + 1
	expected.Header.ParentHash = parent.Hash
	if expected.Header.Timestamp.IsZero() {
		expected.Header.Timestamp = time.Now().UTC()
	}
	expected.Header.TxRoot = transactionsRoot(normalizedTxs)
	expected.Header.CrawlRoot = crawlProofsRoot(normalizedProofs)
	expected.Header.StateRoot = computeStateRoot(snapshot)
	expected.Hash = computeBlockHash(expected)

	if block.Hash != "" {
		if expected.Header.TxRoot != block.Header.TxRoot ||
			expected.Header.CrawlRoot != block.Header.CrawlRoot ||
			expected.Header.StateRoot != block.Header.StateRoot ||
			expected.Hash != block.Hash {
			return preparedBlock{}, ErrInvalidBlock
		}
	}

	for index := range expected.Body.CrawlProofs {
		record := snapshot.SearchIndex[expected.Body.CrawlProofs[index].URL]
		record.BlockHash = expected.Hash
		snapshot.SearchIndex[expected.Body.CrawlProofs[index].URL] = record
	}

	return preparedBlock{
		Block: expected,
		State: snapshot,
		Work:  work,
	}, nil
}

func computeBlockWork(stateAfterTransactions *StateSnapshot, transactions []Transaction, proofs []CrawlProof) (*big.Int, error) {
	work := big.NewInt(0)
	for _, tx := range transactions {
		txWork := uint64(1)
		if tx.Type == TxTypeSearchTask {
			txWork = searchTaskTxWorkUnits
		}
		work.Add(work, new(big.Int).SetUint64(txWork))
	}

	hostCounts := make(map[string]int)
	submitterCounts := make(map[string]int)
	for _, proof := range proofs {
		record, ok := stateAfterTransactions.PendingTasks[strings.TrimSpace(proof.TaskID)]
		if !ok {
			return nil, ErrTaskNotFound
		}
		hostCounts[normalizeWorkHost(proof.URL)]++
		submitterCounts[strings.TrimSpace(record.Submitter)]++
	}
	for _, proof := range proofs {
		record, ok := stateAfterTransactions.PendingTasks[strings.TrimSpace(proof.TaskID)]
		if !ok {
			return nil, ErrTaskNotFound
		}
		proofWork := computeUsefulProofWork(
			record,
			proof,
			hostCounts[normalizeWorkHost(proof.URL)],
			submitterCounts[strings.TrimSpace(record.Submitter)],
		)
		work.Add(work, proofWork)
	}
	return work, nil
}

func computeUsefulProofWork(record SearchTaskRecord, proof CrawlProof, hostOccurrences, submitterOccurrences int) *big.Int {
	difficulty := record.Difficulty
	if difficulty == 0 {
		difficulty = 1
	}
	base := new(big.Int).SetUint64(difficulty)

	qualityBPS := usefulProofQualityBPS(proof)
	hostPenalty := occurrencePenaltyBPS(hostOccurrences, maxProofWorkHostPenaltyFloorBPS)
	submitterPenalty := occurrencePenaltyBPS(submitterOccurrences, maxProofWorkSubmitterPenaltyFloorBPS)
	composite := constants.ComposeBasisPoints(qualityBPS, hostPenalty, submitterPenalty)
	weighted := constants.ApplyBasisPoints(base, composite)
	if weighted.Sign() <= 0 {
		return big.NewInt(1)
	}
	return weighted
}

func usefulProofQualityBPS(proof CrawlProof) uint64 {
	text := strings.TrimSpace(proof.Page.Title + " " + proof.Page.Snippet + " " + proof.Page.Body)
	uniqueTerms := uint64(len(computeTermCounts(text)))
	if uniqueTerms > maxProofWorkUniqueTerms {
		uniqueTerms = maxProofWorkUniqueTerms
	}

	bodyBytes := len(proof.Page.Body) + len(proof.Page.Title) + len(proof.Page.Snippet)
	if bodyBytes > maxProofWorkBodyBytes {
		bodyBytes = maxProofWorkBodyBytes
	}
	bodyBoost := uint64(bodyBytes/proofWorkBodyChunkBytes) * 10
	if bodyBoost > maxProofWorkBodyBoostBPS {
		bodyBoost = maxProofWorkBodyBoostBPS
	}

	return constants.BasisPoints + uniqueTerms*proofWorkTermBoostBPS + bodyBoost
}

func occurrencePenaltyBPS(count int, floor uint64) uint64 {
	if count <= 1 {
		return constants.BasisPoints
	}
	penalty := constants.BasisPoints / uint64(count)
	if penalty < floor {
		return floor
	}
	return penalty
}

func normalizeWorkHost(rawURL string) string {
	cleaned := strings.TrimSpace(strings.ToLower(rawURL))
	if cleaned == "" {
		return ""
	}
	parsed, err := url.Parse(cleaned)
	if err == nil && parsed.Host != "" {
		return strings.ToLower(parsed.Hostname())
	}
	return cleaned
}

func isPreferredTip(candidate, current BlockMeta) bool {
	candidateWork := candidate.CumulativeWorkBig()
	currentWork := current.CumulativeWorkBig()
	if cmp := candidateWork.Cmp(currentWork); cmp != 0 {
		return cmp > 0
	}
	if candidate.Number != current.Number {
		return candidate.Number > current.Number
	}
	return strings.Compare(candidate.Hash, current.Hash) < 0
}

func ApplyTransaction(state *StateSnapshot, tx Transaction) (AppliedTransaction, error) {
	return ApplyTransactionWithArchitect(state, tx, constants.GenesisArchitectAddress)
}

func ApplyTransactionWithArchitect(state *StateSnapshot, tx Transaction, architectAddress string) (AppliedTransaction, error) {
	normalized, err := normalizeTransaction(tx)
	if err != nil {
		return AppliedTransaction{}, err
	}
	if err := normalized.ValidateSignature(); err != nil {
		return AppliedTransaction{}, err
	}

	value, err := normalized.ValueBig()
	if err != nil {
		return AppliedTransaction{}, err
	}
	if value.Sign() <= 0 {
		return AppliedTransaction{}, ErrInvalidTransaction
	}

	expectedNonce := state.Nonces[normalized.From]
	if normalized.Nonce != expectedNonce {
		return AppliedTransaction{}, ErrInvalidNonce
	}

	if balance := state.Balances[normalized.From]; cloneBigInt(balance).Cmp(value) < 0 {
		return AppliedTransaction{}, ErrInsufficientBalance
	}

	architectFee := constants.ArchitectFee(value)
	netValue := new(big.Int).Sub(cloneBigInt(value), architectFee)
	architectAddress = normalizedArchitectAddress(architectAddress)

	debitBalance(state.Balances, normalized.From, value)
	creditBalance(state.Balances, architectAddress, architectFee)
	state.Nonces[normalized.From] = normalized.Nonce + 1

	result := AppliedTransaction{
		Transaction:  normalized,
		ArchitectFee: architectFee,
		NetValue:     netValue,
	}

	switch normalized.Type {
	case TxTypeTransfer:
		if IsNativeContractAddress(normalized.To) {
			if err := ExecuteNativeTransfer(state, normalized, value, netValue); err != nil {
				return AppliedTransaction{}, err
			}
			break
		}
		if err := validateNonSystemAddressWithArchitect(normalized.To, architectAddress); err != nil {
			return AppliedTransaction{}, err
		}
		creditBalance(state.Balances, normalized.To, netValue)
	case TxTypeSearchTask:
		request, err := normalized.SearchTaskRequest()
		if err != nil {
			return AppliedTransaction{}, err
		}
		creditBalance(state.Balances, SearchEscrowAddress, netValue)
		record := SearchTaskRecord{
			ID:              normalized.Hash,
			TxHash:          normalized.Hash,
			Submitter:       normalized.From,
			Query:           request.Query,
			URL:             request.URL,
			BaseBounty:      request.BaseBounty,
			GrossBounty:     value.String(),
			ArchitectFee:    architectFee.String(),
			MinerReward:     netValue.String(),
			Difficulty:      request.Difficulty,
			DataVolumeBytes: request.DataVolumeBytes,
			CreatedAt:       normalized.Timestamp,
		}
		state.PendingTasks[record.ID] = record
		result.Task = &record
	default:
		return AppliedTransaction{}, ErrInvalidTransaction
	}

	return result, nil
}

func ApplyCrawlProof(state *StateSnapshot, proof CrawlProof, blockHash string) (CrawlProof, error) {
	normalized, err := normalizeCrawlProof(proof)
	if err != nil {
		return CrawlProof{}, err
	}

	task, ok := state.PendingTasks[normalized.TaskID]
	if !ok {
		return CrawlProof{}, ErrTaskNotFound
	}
	if task.Completed {
		return CrawlProof{}, ErrTaskSettled
	}

	expectedGross, _ := new(big.Int).SetString(task.GrossBounty, 10)
	expectedFee, _ := new(big.Int).SetString(task.ArchitectFee, 10)
	expectedReward, _ := new(big.Int).SetString(task.MinerReward, 10)
	actualGross, _ := new(big.Int).SetString(normalized.GrossBounty, 10)
	actualFee, _ := new(big.Int).SetString(normalized.ArchitectFee, 10)
	actualReward, _ := new(big.Int).SetString(normalized.MinerReward, 10)

	if expectedGross.Cmp(actualGross) != 0 ||
		expectedFee.Cmp(actualFee) != 0 ||
		expectedReward.Cmp(actualReward) != 0 {
		return CrawlProof{}, ErrInvalidTransactionFee
	}
	if cloneBigInt(state.Balances[SearchEscrowAddress]).Cmp(actualReward) < 0 {
		return CrawlProof{}, ErrInsufficientBalance
	}

	debitBalance(state.Balances, SearchEscrowAddress, actualReward)
	creditBalance(state.Balances, normalized.Miner, actualReward)

	record := SearchRecord{
		TaskID:      normalized.TaskID,
		URL:         normalized.URL,
		Query:       normalized.Query,
		Title:       normalized.Page.Title,
		Snippet:     normalized.Page.Snippet,
		Body:        normalized.Page.Body,
		ContentHash: normalized.Page.ContentHash,
		SimHash:     normalized.Page.SimHash,
		IndexedAt:   normalized.CreatedAt,
		IndexedBy:   normalized.Miner,
		ProofHash:   normalized.ProofHash,
		BlockHash:   blockHash,
		TermCounts:  computeTermCounts(normalized.Query + " " + normalized.Page.Title + " " + normalized.Page.Body),
	}

	if existing, ok := state.SearchIndex[normalized.URL]; ok {
		subtractMentionCounts(state.MentionCounts, existing.TermCounts)
	}
	addMentionCounts(state.MentionCounts, record.TermCounts)
	state.SearchIndex[normalized.URL] = record

	task.Completed = true
	task.CompletedAt = normalized.CreatedAt
	task.ProofHash = normalized.ProofHash
	task.MinedBy = normalized.Miner
	state.PendingTasks[task.ID] = task
	for _, spawned := range buildAutonomousSearchTaskRecords(state, task, normalized) {
		state.PendingTasks[spawned.ID] = spawned
	}
	return normalized, nil
}

func BuildSearchTaskEnvelope(tx Transaction, request SearchTaskRequest, registry *consensus.PriorityRegistry) (SearchTaskEnvelope, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return SearchTaskEnvelope{}, err
	}

	tx.Data = payload
	normalized, err := normalizeTransaction(tx)
	if err != nil {
		return SearchTaskEnvelope{}, err
	}
	if normalized.Type != TxTypeSearchTask {
		return SearchTaskEnvelope{}, ErrInvalidTransaction
	}
	if err := normalized.ValidateSignature(); err != nil {
		return SearchTaskEnvelope{}, err
	}

	value, err := normalized.ValueBig()
	if err != nil {
		return SearchTaskEnvelope{}, err
	}
	baseBounty, ok := new(big.Int).SetString(strings.TrimSpace(request.BaseBounty), 10)
	if !ok || baseBounty.Sign() < 0 {
		return SearchTaskEnvelope{}, ErrInvalidTransaction
	}

	task, err := consensus.NewCrawlTask(request.Query, []string{request.URL}, baseBounty, request.Difficulty, request.DataVolumeBytes, 0)
	if err != nil {
		return SearchTaskEnvelope{}, err
	}

	adjustment, err := registry.Apply(task, request.URL)
	if err != nil {
		return SearchTaskEnvelope{}, err
	}
	if adjustment.AdjustedBounty.Cmp(value) != 0 {
		return SearchTaskEnvelope{}, fmt.Errorf("%w: tx value does not match quoted search bounty", ErrInvalidTransaction)
	}

	task.ID = normalized.Hash
	task.TotalBounty = cloneBigInt(value)
	task.ArchitectFee = cloneBigInt(adjustment.ArchitectFee)
	task.MinerReward = cloneBigInt(adjustment.NetMinerReward)
	task.Difficulty = adjustment.AdjustedDifficulty
	task.AdjustedDifficulty = adjustment.AdjustedDifficulty
	task.GovernanceMultiplierBPS = adjustment.MultiplierBPS
	task.PrioritySectors = append([]string(nil), adjustment.PrioritySectors...)
	return SearchTaskEnvelope{
		Transaction: normalized,
		Request:     request,
		Task:        task,
	}, nil
}

func (tx Transaction) ValueBig() (*big.Int, error) {
	value, ok := new(big.Int).SetString(strings.TrimSpace(tx.Value), 10)
	if !ok {
		return nil, ErrInvalidTransaction
	}
	return value, nil
}

func (tx Transaction) SearchTaskRequest() (SearchTaskRequest, error) {
	var request SearchTaskRequest
	if tx.Type != TxTypeSearchTask {
		return request, ErrInvalidTransaction
	}
	if len(tx.Data) == 0 {
		return request, ErrInvalidTransaction
	}
	if err := json.Unmarshal(tx.Data, &request); err != nil {
		return request, err
	}
	if strings.TrimSpace(request.Query) == "" || strings.TrimSpace(request.URL) == "" {
		return request, ErrInvalidTransaction
	}
	if _, ok := new(big.Int).SetString(strings.TrimSpace(request.BaseBounty), 10); !ok {
		return request, ErrInvalidTransaction
	}
	return request, nil
}

func (tx Transaction) ValidateSignature() error {
	if tx.Type != TxTypeTransfer && tx.Type != TxTypeSearchTask {
		return ErrInvalidTransaction
	}
	if err := validateNonSystemAddress(tx.From); err != nil {
		return err
	}
	if len(tx.PublicKey) != ed25519.PublicKeySize || len(tx.Signature) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	derived, err := types.NewAddressFromPubKey(tx.PublicKey)
	if err != nil {
		return err
	}
	if derived.String() != tx.From {
		return ErrInvalidSignature
	}
	if !ed25519.Verify(ed25519.PublicKey(tx.PublicKey), tx.signingPayload(), tx.Signature) {
		return ErrInvalidSignature
	}
	expectedHash := computeTransactionHash(tx)
	if tx.Hash != expectedHash {
		return ErrInvalidTransaction
	}
	return nil
}

func (tx *Transaction) Sign(privateKey ed25519.PrivateKey) error {
	if tx.Timestamp.IsZero() {
		tx.Timestamp = time.Now().UTC()
	}
	tx.PublicKey = append([]byte(nil), privateKey.Public().(ed25519.PublicKey)...)
	tx.Signature = ed25519.Sign(privateKey, tx.signingPayload())
	tx.Hash = computeTransactionHash(*tx)
	return nil
}

func normalizeTransaction(tx Transaction) (Transaction, error) {
	normalized := tx
	normalized.From = strings.TrimSpace(normalized.From)
	normalized.To = strings.TrimSpace(normalized.To)
	if normalized.Timestamp.IsZero() {
		normalized.Timestamp = time.Now().UTC()
	}
	normalized.Hash = computeTransactionHash(normalized)
	return normalized, nil
}

func normalizeCrawlProof(proof CrawlProof) (CrawlProof, error) {
	normalized := proof
	normalized.TaskID = strings.TrimSpace(normalized.TaskID)
	normalized.TaskTxHash = strings.TrimSpace(normalized.TaskTxHash)
	normalized.URL = strings.TrimSpace(normalized.URL)
	normalized.Query = strings.TrimSpace(normalized.Query)
	normalized.Miner = strings.TrimSpace(normalized.Miner)
	if normalized.TaskID == "" || normalized.URL == "" || normalized.Miner == "" {
		return CrawlProof{}, ErrInvalidBlock
	}
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = time.Now().UTC()
	}
	if normalized.ProofHash == "" {
		normalized.ProofHash = computeCrawlProofHash(normalized)
	}
	if normalized.ProofHash != computeCrawlProofHash(normalized) {
		return CrawlProof{}, ErrInvalidBlock
	}
	return normalized, nil
}

func buildAutonomousSearchTaskRecords(state *StateSnapshot, parent SearchTaskRecord, proof CrawlProof) []SearchTaskRecord {
	if state == nil {
		return nil
	}
	if parent.Autonomous && parent.Depth >= autonomousCrawlMaxDepth {
		return nil
	}
	if len(proof.Page.OutboundLinks) == 0 {
		return nil
	}

	activeAutonomous := countActiveAutonomousSearchTasks(state)
	if activeAutonomous >= autonomousCrawlPendingTaskLimit {
		return nil
	}

	candidates := selectAutonomousOutboundLinks(state, parent, proof)
	if len(candidates) == 0 {
		return nil
	}

	remainingSlots := autonomousCrawlPendingTaskLimit - activeAutonomous
	limit := autonomousCrawlLinksPerProof
	if remainingSlots < limit {
		limit = remainingSlots
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	nextDepth := parent.Depth + 1
	originTxHash := strings.TrimSpace(parent.TxHash)
	if originTxHash == "" {
		originTxHash = parent.ID
	}

	spawned := make([]SearchTaskRecord, 0, len(candidates))
	for _, target := range candidates {
		record := SearchTaskRecord{
			ID:              buildAutonomousSearchTaskID(parent.ID, target, nextDepth),
			TxHash:          originTxHash,
			Submitter:       parent.Submitter,
			Query:           parent.Query,
			URL:             target,
			BaseBounty:      "0",
			GrossBounty:     "0",
			ArchitectFee:    "0",
			MinerReward:     "0",
			Difficulty:      0,
			DataVolumeBytes: 0,
			CreatedAt:       proof.CreatedAt,
			Autonomous:      true,
			ParentTaskID:    parent.ID,
			Depth:           nextDepth,
		}
		spawned = append(spawned, record)
	}
	return spawned
}

func selectAutonomousOutboundLinks(state *StateSnapshot, parent SearchTaskRecord, proof CrawlProof) []string {
	type candidate struct {
		url          string
		sameHost     bool
		hasQuery     bool
		pathSegments int
		length       int
	}

	parentURL, err := url.Parse(strings.TrimSpace(proof.URL))
	if err != nil {
		parentURL, _ = url.Parse(strings.TrimSpace(parent.URL))
	}
	parentHost := ""
	if parentURL != nil {
		parentHost = strings.ToLower(parentURL.Hostname())
	}

	candidates := make([]candidate, 0, len(proof.Page.OutboundLinks))
	seen := make(map[string]struct{}, len(proof.Page.OutboundLinks))
	for _, raw := range proof.Page.OutboundLinks {
		target, err := normalizeSearchTaskURL(raw)
		if err != nil {
			continue
		}
		if target == strings.TrimSpace(proof.URL) || target == strings.TrimSpace(parent.URL) {
			continue
		}
		if shouldSkipAutonomousURL(target) {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		if _, ok := state.SearchIndex[target]; ok {
			continue
		}
		if stateHasActiveSearchTaskURL(state, target) {
			continue
		}

		parsed, err := url.Parse(target)
		if err != nil {
			continue
		}
		seen[target] = struct{}{}
		candidates = append(candidates, candidate{
			url:          target,
			sameHost:     parentHost != "" && strings.EqualFold(parsed.Hostname(), parentHost),
			hasQuery:     parsed.RawQuery != "",
			pathSegments: countURLPathSegments(parsed.Path),
			length:       len(target),
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].sameHost != candidates[j].sameHost {
			return candidates[i].sameHost
		}
		if candidates[i].hasQuery != candidates[j].hasQuery {
			return !candidates[i].hasQuery
		}
		if candidates[i].pathSegments != candidates[j].pathSegments {
			return candidates[i].pathSegments < candidates[j].pathSegments
		}
		if candidates[i].length != candidates[j].length {
			return candidates[i].length < candidates[j].length
		}
		return candidates[i].url < candidates[j].url
	})

	out := make([]string, 0, len(candidates))
	for _, item := range candidates {
		out = append(out, item.url)
	}
	return out
}

func buildAutonomousSearchTaskID(parentTaskID, target string, depth uint64) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(parentTaskID),
		strconv.FormatUint(depth, 10),
		strings.TrimSpace(target),
	}, "|")))
	return hex.EncodeToString(sum[:16])
}

func normalizeSearchTaskURL(raw string) (string, error) {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return "", ErrInvalidTransaction
	}

	target.Fragment = ""
	target.Scheme = strings.ToLower(target.Scheme)
	target.Host = strings.ToLower(target.Host)
	if target.Scheme != "http" && target.Scheme != "https" {
		return "", ErrInvalidTransaction
	}
	return target.String(), nil
}

func shouldSkipAutonomousURL(target string) bool {
	parsed, err := url.Parse(target)
	if err != nil {
		return true
	}
	switch strings.ToLower(path.Ext(parsed.EscapedPath())) {
	case ".7z", ".avi", ".bin", ".bmp", ".css", ".csv", ".doc", ".docx", ".gif", ".gz",
		".ico", ".jpeg", ".jpg", ".js", ".json", ".m4a", ".mov", ".mp3", ".mp4", ".pdf",
		".png", ".ppt", ".pptx", ".rss", ".svg", ".tar", ".tgz", ".txt", ".wav", ".webm",
		".webp", ".woff", ".woff2", ".xls", ".xlsx", ".xml", ".zip":
		return true
	}
	return false
}

func countURLPathSegments(rawPath string) int {
	trimmed := strings.Trim(rawPath, "/")
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "/"))
}

func stateHasActiveSearchTaskURL(state *StateSnapshot, target string) bool {
	for _, task := range state.PendingTasks {
		if task.Completed {
			continue
		}
		if task.URL == target {
			return true
		}
	}
	return false
}

func countActiveAutonomousSearchTasks(state *StateSnapshot) int {
	count := 0
	for _, task := range state.PendingTasks {
		if task.Completed || !task.Autonomous {
			continue
		}
		count++
	}
	return count
}

func sortPendingSearchTaskRecords(records []SearchTaskRecord) {
	sort.Slice(records, func(i, j int) bool {
		leftValue := searchTaskRecordGrossBounty(records[i])
		rightValue := searchTaskRecordGrossBounty(records[j])
		if cmp := leftValue.Cmp(rightValue); cmp != 0 {
			return cmp > 0
		}
		if records[i].Depth != records[j].Depth {
			return records[i].Depth < records[j].Depth
		}
		if !records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].CreatedAt.Before(records[j].CreatedAt)
		}
		return records[i].ID < records[j].ID
	})
}

func searchTaskRecordGrossBounty(record SearchTaskRecord) *big.Int {
	value, ok := new(big.Int).SetString(strings.TrimSpace(record.GrossBounty), 10)
	if !ok {
		return big.NewInt(0)
	}
	return value
}

func (tx Transaction) signingPayload() []byte {
	payload, _ := json.Marshal(struct {
		Type      TxType    `json:"type"`
		From      string    `json:"from"`
		To        string    `json:"to,omitempty"`
		Value     string    `json:"value"`
		Nonce     uint64    `json:"nonce"`
		Timestamp time.Time `json:"timestamp"`
		Data      []byte    `json:"data,omitempty"`
		PublicKey []byte    `json:"publicKey,omitempty"`
	}{
		Type:      tx.Type,
		From:      tx.From,
		To:        tx.To,
		Value:     tx.Value,
		Nonce:     tx.Nonce,
		Timestamp: tx.Timestamp.UTC(),
		Data:      tx.Data,
		PublicKey: tx.PublicKey,
	})
	return payload
}

func computeTransactionHash(tx Transaction) string {
	payload, _ := json.Marshal(struct {
		Type      TxType    `json:"type"`
		From      string    `json:"from"`
		To        string    `json:"to,omitempty"`
		Value     string    `json:"value"`
		Nonce     uint64    `json:"nonce"`
		Timestamp time.Time `json:"timestamp"`
		Data      []byte    `json:"data,omitempty"`
		PublicKey []byte    `json:"publicKey,omitempty"`
		Signature []byte    `json:"signature,omitempty"`
	}{
		Type:      tx.Type,
		From:      tx.From,
		To:        tx.To,
		Value:     tx.Value,
		Nonce:     tx.Nonce,
		Timestamp: tx.Timestamp.UTC(),
		Data:      tx.Data,
		PublicKey: tx.PublicKey,
		Signature: tx.Signature,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func computeCrawlProofHash(proof CrawlProof) string {
	payload, _ := json.Marshal(struct {
		TaskID      string `json:"taskId"`
		URL         string `json:"url"`
		ContentHash string `json:"contentHash"`
		SimHash     uint64 `json:"simHash"`
	}{
		TaskID:      proof.TaskID,
		URL:         proof.URL,
		ContentHash: proof.Page.ContentHash,
		SimHash:     proof.Page.SimHash,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func computeBlockHash(block Block) string {
	payload, _ := json.Marshal(block.Header)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func transactionsRoot(transactions []Transaction) string {
	hashes := make([]string, 0, len(transactions))
	for _, tx := range transactions {
		hashes = append(hashes, tx.Hash)
	}
	return merkleRoot(hashes)
}

func crawlProofsRoot(proofs []CrawlProof) string {
	hashes := make([]string, 0, len(proofs))
	for _, proof := range proofs {
		hashes = append(hashes, proof.ProofHash)
	}
	return merkleRoot(hashes)
}

func merkleRoot(values []string) string {
	if len(values) == 0 {
		sum := sha256.Sum256(nil)
		return hex.EncodeToString(sum[:])
	}
	nodes := append([]string(nil), values...)
	for len(nodes) > 1 {
		next := make([]string, 0, (len(nodes)+1)/2)
		for i := 0; i < len(nodes); i += 2 {
			left := nodes[i]
			right := left
			if i+1 < len(nodes) {
				right = nodes[i+1]
			}
			sum := sha256.Sum256([]byte(left + right))
			next = append(next, hex.EncodeToString(sum[:]))
		}
		nodes = next
	}
	return nodes[0]
}

func computeStateRoot(state *StateSnapshot) string {
	entries := make([]string, 0, len(state.Balances)+len(state.Nonces)+len(state.PendingTasks)+len(state.SearchIndex)+len(state.MentionCounts)+len(state.Names))

	addresses := make([]string, 0, len(state.Balances))
	for address := range state.Balances {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		entries = append(entries, "bal:"+address+"="+cloneBigInt(state.Balances[address]).String())
	}

	nonceKeys := make([]string, 0, len(state.Nonces))
	for address := range state.Nonces {
		nonceKeys = append(nonceKeys, address)
	}
	sort.Strings(nonceKeys)
	for _, address := range nonceKeys {
		entries = append(entries, "nonce:"+address+"="+strconv.FormatUint(state.Nonces[address], 10))
	}

	taskKeys := make([]string, 0, len(state.PendingTasks))
	for id := range state.PendingTasks {
		taskKeys = append(taskKeys, id)
	}
	sort.Strings(taskKeys)
	for _, id := range taskKeys {
		payload, _ := json.Marshal(state.PendingTasks[id])
		entries = append(entries, "task:"+id+"="+string(payload))
	}

	searchKeys := make([]string, 0, len(state.SearchIndex))
	for url := range state.SearchIndex {
		searchKeys = append(searchKeys, url)
	}
	sort.Strings(searchKeys)
	for _, url := range searchKeys {
		record := state.SearchIndex[url]
		record.BlockHash = ""
		payload, _ := json.Marshal(record)
		entries = append(entries, "search:"+url+"="+string(payload))
	}

	mentionKeys := make([]string, 0, len(state.MentionCounts))
	for term := range state.MentionCounts {
		mentionKeys = append(mentionKeys, term)
	}
	sort.Strings(mentionKeys)
	for _, term := range mentionKeys {
		entries = append(entries, "mention:"+term+"="+strconv.FormatUint(state.MentionCounts[term], 10))
	}

	nameKeys := make([]string, 0, len(state.Names))
	for name := range state.Names {
		nameKeys = append(nameKeys, name)
	}
	sort.Strings(nameKeys)
	for _, name := range nameKeys {
		payload, _ := json.Marshal(state.Names[name])
		entries = append(entries, "name:"+name+"="+string(payload))
	}

	sum := sha256.Sum256([]byte(strings.Join(entries, "|")))
	return hex.EncodeToString(sum[:])
}

func (bc *Blockchain) loadLocked() error {
	bc.state = NewStateSnapshot()

	if err := bc.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(metaNetworkConfigKey))
		if err == nil {
			if err := item.Value(func(value []byte) error {
				var cfg NetworkConfig
				if err := json.Unmarshal(value, &cfg); err != nil {
					return err
				}
				normalized, err := NormalizeNetworkConfig(cfg)
				if err != nil {
					return err
				}
				bc.network = normalized
				return nil
			}); err != nil {
				return err
			}
		} else if !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}

		var latestHash string
		item, err = txn.Get([]byte(metaLatestHashKey))
		if err == nil {
			if err := item.Value(func(value []byte) error {
				latestHash = strings.TrimSpace(string(value))
				return nil
			}); err != nil {
				return err
			}
		} else if !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}

		if latestHash != "" {
			latest, err := loadBlockTxn(txn, latestHash)
			if err != nil && !errors.Is(err, ErrBlockNotFound) {
				return err
			}
			if err == nil {
				latestMeta, metaErr := loadBlockMetaTxn(txn, latestHash)
				if metaErr != nil && !errors.Is(metaErr, ErrBlockNotFound) {
					return metaErr
				}
				latestState, stateErr := loadStateSnapshotTxn(txn, latestHash)
				if stateErr != nil && !errors.Is(stateErr, ErrBlockNotFound) {
					return stateErr
				}
				if stateErr == nil {
					bc.latest = latest
					if metaErr == nil {
						bc.latestMeta = latestMeta
					} else {
						bc.latestMeta = syntheticLatestMeta(latest)
					}
					bc.state = latestState
					return nil
				}
			}
		}

		if err := iteratePrefix(txn, []byte(balancePrefix), func(key, value []byte) error {
			amount, ok := new(big.Int).SetString(string(value), 10)
			if !ok {
				return ErrInvalidBlock
			}
			bc.state.Balances[strings.TrimPrefix(string(key), balancePrefix)] = amount
			return nil
		}); err != nil {
			return err
		}
		if err := iteratePrefix(txn, []byte(noncePrefix), func(key, value []byte) error {
			nonce, err := strconv.ParseUint(string(value), 10, 64)
			if err != nil {
				return err
			}
			bc.state.Nonces[strings.TrimPrefix(string(key), noncePrefix)] = nonce
			return nil
		}); err != nil {
			return err
		}
		if err := iteratePrefix(txn, []byte(searchPrefix), func(_, value []byte) error {
			var record SearchRecord
			if err := decodeStoredJSON(value, &record); err != nil {
				return err
			}
			if record.TermCounts == nil {
				record.TermCounts = map[string]uint64{}
			}
			bc.state.SearchIndex[record.URL] = record
			return nil
		}); err != nil {
			return err
		}
		if err := iteratePrefix(txn, []byte(mentionPrefix), func(key, value []byte) error {
			count, err := strconv.ParseUint(string(value), 10, 64)
			if err != nil {
				return err
			}
			bc.state.MentionCounts[strings.TrimPrefix(string(key), mentionPrefix)] = count
			return nil
		}); err != nil {
			return err
		}
		if err := iteratePrefix(txn, []byte(taskPrefix), func(_, value []byte) error {
			var record SearchTaskRecord
			if err := decodeStoredJSON(value, &record); err != nil {
				return err
			}
			bc.state.PendingTasks[record.ID] = record
			return nil
		}); err != nil {
			return err
		}
		if err := iteratePrefix(txn, []byte(namePrefix), func(_, value []byte) error {
			var record NameRecord
			if err := decodeStoredJSON(value, &record); err != nil {
				return err
			}
			bc.state.Names[record.Name] = record
			return nil
		}); err != nil {
			return err
		}

		item, err = txn.Get([]byte(metaLatestBlockKey))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return nil
			}
			return err
		}

		var latestNumber uint64
		if err := item.Value(func(value []byte) error {
			parsed, err := strconv.ParseUint(string(value), 10, 64)
			if err != nil {
				return err
			}
			latestNumber = parsed
			return nil
		}); err != nil {
			return err
		}

		item, err = txn.Get([]byte(blockNumberKey(latestNumber)))
		if err != nil {
			return err
		}
		if err := item.Value(func(value []byte) error {
			return decodeStoredJSON(value, &bc.latest)
		}); err != nil {
			return err
		}

		meta, err := loadBlockMetaTxn(txn, bc.latest.Hash)
		if err != nil {
			if !errors.Is(err, ErrBlockNotFound) {
				return err
			}
			bc.latestMeta = syntheticLatestMeta(bc.latest)
			return nil
		}
		bc.latestMeta = meta
		return nil
	}); err != nil {
		return err
	}

	return nil
}

func (bc *Blockchain) persistCanonicalTipLocked(state *StateSnapshot, block Block, meta BlockMeta) error {
	if err := bc.db.Update(func(txn *badger.Txn) error {
		if err := persistBlockArtifacts(txn, state, block, meta); err != nil {
			return err
		}
		if err := rewriteCanonicalTip(txn, bc.latestMeta.Hash, meta.Hash); err != nil {
			return err
		}
		if err := persistNetworkConfig(txn, bc.network); err != nil {
			return err
		}
		if err := txn.Set([]byte(metaLatestHashKey), []byte(block.Hash)); err != nil {
			return err
		}
		if err := txn.Set([]byte(metaLatestBlockKey), []byte(strconv.FormatUint(block.Header.Number, 10))); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	bc.state = state
	bc.latest = block
	bc.latestMeta = meta
	return nil
}

func (bc *Blockchain) persistStateOnlyLocked() error {
	return bc.db.Update(func(txn *badger.Txn) error {
		if err := persistNetworkConfig(txn, bc.network); err != nil {
			return err
		}
		if strings.TrimSpace(bc.latest.Hash) == "" {
			return nil
		}
		if err := txn.Set([]byte(metaLatestHashKey), []byte(bc.latest.Hash)); err != nil {
			return err
		}
		if err := txn.Set([]byte(metaLatestBlockKey), []byte(strconv.FormatUint(bc.latest.Header.Number, 10))); err != nil {
			return err
		}
		return persistStateSnapshot(txn, bc.latest.Hash, bc.state)
	})
}

func (bc *Blockchain) storeImportedBlockLocked(state *StateSnapshot, block Block, meta BlockMeta, canonical bool) error {
	if err := bc.db.Update(func(txn *badger.Txn) error {
		if err := persistBlockArtifacts(txn, state, block, meta); err != nil {
			return err
		}
		if !canonical {
			return nil
		}
		if err := rewriteCanonicalTip(txn, bc.latestMeta.Hash, meta.Hash); err != nil {
			return err
		}
		if err := persistNetworkConfig(txn, bc.network); err != nil {
			return err
		}
		if err := txn.Set([]byte(metaLatestHashKey), []byte(block.Hash)); err != nil {
			return err
		}
		return txn.Set([]byte(metaLatestBlockKey), []byte(strconv.FormatUint(block.Header.Number, 10)))
	}); err != nil {
		return err
	}

	if canonical {
		bc.state = state
		bc.latest = block
		bc.latestMeta = meta
	}
	return nil
}

func persistBlockArtifacts(txn *badger.Txn, state *StateSnapshot, block Block, meta BlockMeta) error {
	blockPayload, err := encodeStoredJSON(block)
	if err != nil {
		return err
	}
	if err := txn.Set([]byte(blockHashKey(block.Hash)), blockPayload); err != nil {
		return err
	}
	metaPayload, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := txn.Set([]byte(blockMetaKey(block.Hash)), metaPayload); err != nil {
		return err
	}
	for _, tx := range block.Body.Transactions {
		payload, err := json.Marshal(tx)
		if err != nil {
			return err
		}
		if err := txn.Set([]byte(txPrefix+tx.Hash), payload); err != nil {
			return err
		}
	}
	return persistStateSnapshot(txn, block.Hash, state)
}

func persistStateSnapshot(txn *badger.Txn, blockHash string, state *StateSnapshot) error {
	payload, err := marshalStateSnapshot(state)
	if err != nil {
		return err
	}
	payload, err = compressForStorage(payload)
	if err != nil {
		return err
	}
	return txn.Set([]byte(stateSnapshotKey(blockHash)), payload)
}

func persistNetworkConfig(txn *badger.Txn, config NetworkConfig) error {
	normalized, err := NormalizeNetworkConfig(config)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	return txn.Set([]byte(metaNetworkConfigKey), payload)
}

func rewriteCanonicalTip(txn *badger.Txn, oldTipHash, newTipHash string) error {
	if strings.TrimSpace(newTipHash) == "" {
		return ErrInvalidBlock
	}

	oldPath, newPath, err := reorgPaths(txn, oldTipHash, newTipHash)
	if err != nil {
		return err
	}

	for _, meta := range oldPath {
		meta.Canonical = false
		if err := storeBlockMetaTxn(txn, meta); err != nil {
			return err
		}
		if err := txn.Delete([]byte(blockNumberKey(meta.Number))); err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}
	}

	for index := len(newPath) - 1; index >= 0; index-- {
		meta := newPath[index]
		meta.Canonical = true
		if err := storeBlockMetaTxn(txn, meta); err != nil {
			return err
		}
		block, err := loadBlockTxn(txn, meta.Hash)
		if err != nil {
			return err
		}
		payload, err := encodeStoredJSON(block)
		if err != nil {
			return err
		}
		if err := txn.Set([]byte(blockNumberKey(meta.Number)), payload); err != nil {
			return err
		}
	}

	return nil
}

func reorgPaths(txn *badger.Txn, oldTipHash, newTipHash string) ([]BlockMeta, []BlockMeta, error) {
	if strings.TrimSpace(newTipHash) == "" {
		return nil, nil, ErrInvalidBlock
	}
	if strings.TrimSpace(oldTipHash) == "" {
		newTip, err := loadBlockMetaTxn(txn, newTipHash)
		if err != nil {
			return nil, nil, err
		}
		return nil, []BlockMeta{newTip}, nil
	}

	oldAncestors := make(map[string]struct{})
	current := strings.TrimSpace(oldTipHash)
	for current != "" {
		oldAncestors[current] = struct{}{}
		meta, err := loadBlockMetaTxn(txn, current)
		if err != nil {
			return nil, nil, err
		}
		current = meta.ParentHash
	}

	newPath := make([]BlockMeta, 0)
	commonAncestor := ""
	current = strings.TrimSpace(newTipHash)
	for current != "" {
		meta, err := loadBlockMetaTxn(txn, current)
		if err != nil {
			return nil, nil, err
		}
		if _, ok := oldAncestors[current]; ok {
			commonAncestor = current
			break
		}
		newPath = append(newPath, meta)
		current = meta.ParentHash
	}
	if commonAncestor == "" {
		return nil, nil, ErrInvalidBlock
	}

	oldPath := make([]BlockMeta, 0)
	current = strings.TrimSpace(oldTipHash)
	for current != "" && current != commonAncestor {
		meta, err := loadBlockMetaTxn(txn, current)
		if err != nil {
			return nil, nil, err
		}
		oldPath = append(oldPath, meta)
		current = meta.ParentHash
	}

	return oldPath, newPath, nil
}

func storeBlockMetaTxn(txn *badger.Txn, meta BlockMeta) error {
	payload, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return txn.Set([]byte(blockMetaKey(meta.Hash)), payload)
}

func loadBlockMetaTxn(txn *badger.Txn, hash string) (BlockMeta, error) {
	var meta BlockMeta
	item, err := txn.Get([]byte(blockMetaKey(hash)))
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return BlockMeta{}, ErrBlockNotFound
		}
		return BlockMeta{}, err
	}
	err = item.Value(func(value []byte) error {
		return json.Unmarshal(value, &meta)
	})
	return meta, err
}

func loadBlockTxn(txn *badger.Txn, hash string) (Block, error) {
	var block Block
	item, err := txn.Get([]byte(blockHashKey(hash)))
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return Block{}, ErrBlockNotFound
		}
		return Block{}, err
	}
	err = item.Value(func(value []byte) error {
		return decodeStoredJSON(value, &block)
	})
	return block, err
}

func persistState(txn *badger.Txn, state *StateSnapshot) error {
	for _, prefix := range []string{balancePrefix, noncePrefix, searchPrefix, mentionPrefix, taskPrefix, namePrefix} {
		if err := clearPrefix(txn, []byte(prefix)); err != nil {
			return err
		}
	}

	for address, balance := range state.Balances {
		if err := txn.Set([]byte(balancePrefix+address), []byte(cloneBigInt(balance).String())); err != nil {
			return err
		}
	}
	for address, nonce := range state.Nonces {
		if err := txn.Set([]byte(noncePrefix+address), []byte(strconv.FormatUint(nonce, 10))); err != nil {
			return err
		}
	}
	for url, record := range state.SearchIndex {
		payload, err := encodeStoredJSON(record)
		if err != nil {
			return err
		}
		if err := txn.Set([]byte(searchPrefix+hashKey(url)), payload); err != nil {
			return err
		}
	}
	for term, count := range state.MentionCounts {
		if err := txn.Set([]byte(mentionPrefix+term), []byte(strconv.FormatUint(count, 10))); err != nil {
			return err
		}
	}
	for id, task := range state.PendingTasks {
		payload, err := encodeStoredJSON(task)
		if err != nil {
			return err
		}
		if err := txn.Set([]byte(taskPrefix+id), payload); err != nil {
			return err
		}
	}
	for name, record := range state.Names {
		payload, err := encodeStoredJSON(record)
		if err != nil {
			return err
		}
		if err := txn.Set([]byte(namePrefix+name), payload); err != nil {
			return err
		}
	}
	return nil
}

func clearPrefix(txn *badger.Txn, prefix []byte) error {
	iterator := txn.NewIterator(badger.DefaultIteratorOptions)
	defer iterator.Close()

	for iterator.Seek(prefix); iterator.ValidForPrefix(prefix); iterator.Next() {
		key := append([]byte(nil), iterator.Item().Key()...)
		if err := txn.Delete(key); err != nil {
			return err
		}
	}

	return nil
}

func iteratePrefix(txn *badger.Txn, prefix []byte, fn func(key, value []byte) error) error {
	iterator := txn.NewIterator(badger.DefaultIteratorOptions)
	defer iterator.Close()
	for iterator.Seek(prefix); iterator.ValidForPrefix(prefix); iterator.Next() {
		item := iterator.Item()
		key := append([]byte(nil), item.Key()...)
		if err := item.Value(func(value []byte) error {
			return fn(key, append([]byte(nil), value...))
		}); err != nil {
			return err
		}
	}
	return nil
}

func loadStateSnapshotTxn(txn *badger.Txn, hash string) (*StateSnapshot, error) {
	item, err := txn.Get([]byte(stateSnapshotKey(hash)))
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil, ErrBlockNotFound
		}
		return nil, err
	}

	var snapshot *StateSnapshot
	if err := item.Value(func(value []byte) error {
		loaded, err := unmarshalStateSnapshot(value)
		if err != nil {
			return err
		}
		snapshot = loaded
		return nil
	}); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func encodeStoredJSON(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return compressForStorage(payload)
}

func decodeStoredJSON(payload []byte, target any) error {
	decoded, err := decompressStoredPayload(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(decoded, target)
}

func compressForStorage(payload []byte) ([]byte, error) {
	if len(payload) < storageCompressionThreshold {
		return payload, nil
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if buf.Len() >= len(payload) {
		return payload, nil
	}
	return buf.Bytes(), nil
}

func decompressStoredPayload(payload []byte) ([]byte, error) {
	if len(payload) < 2 || payload[0] != 0x1f || payload[1] != 0x8b {
		return payload, nil
	}

	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	decoded, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func marshalStateSnapshot(state *StateSnapshot) ([]byte, error) {
	disk := stateSnapshotDisk{
		Balances:      make(map[string]string, len(state.Balances)),
		Nonces:        cloneUint64Map(state.Nonces),
		PendingTasks:  make(map[string]SearchTaskRecord, len(state.PendingTasks)),
		SearchIndex:   make(map[string]SearchRecord, len(state.SearchIndex)),
		MentionCounts: cloneUint64Map(state.MentionCounts),
		Names:         make(map[string]NameRecord, len(state.Names)),
	}
	for address, balance := range state.Balances {
		disk.Balances[address] = cloneBigInt(balance).String()
	}
	for id, task := range state.PendingTasks {
		disk.PendingTasks[id] = task
	}
	for url, record := range state.SearchIndex {
		copyRecord := record
		copyRecord.TermCounts = cloneUint64Map(record.TermCounts)
		disk.SearchIndex[url] = copyRecord
	}
	for name, record := range state.Names {
		disk.Names[name] = record
	}
	return json.Marshal(disk)
}

func unmarshalStateSnapshot(data []byte) (*StateSnapshot, error) {
	decoded, err := decompressStoredPayload(data)
	if err != nil {
		return nil, err
	}

	var disk stateSnapshotDisk
	if err := json.Unmarshal(decoded, &disk); err != nil {
		return nil, err
	}
	snapshot := NewStateSnapshot()
	for address, amount := range disk.Balances {
		value, ok := new(big.Int).SetString(amount, 10)
		if !ok {
			return nil, ErrInvalidBlock
		}
		snapshot.Balances[address] = value
	}
	for address, nonce := range disk.Nonces {
		snapshot.Nonces[address] = nonce
	}
	for id, task := range disk.PendingTasks {
		snapshot.PendingTasks[id] = task
	}
	for url, record := range disk.SearchIndex {
		copyRecord := record
		copyRecord.TermCounts = cloneUint64Map(record.TermCounts)
		snapshot.SearchIndex[url] = copyRecord
	}
	for term, count := range disk.MentionCounts {
		snapshot.MentionCounts[term] = count
	}
	for name, record := range disk.Names {
		snapshot.Names[name] = record
	}
	return snapshot, nil
}

func blockNumberKey(number uint64) string {
	return blockNumberPrefix + fmt.Sprintf("%020d", number)
}

func blockHashKey(hash string) string {
	return blockHashPrefix + hash
}

func blockMetaKey(hash string) string {
	return blockMetaPrefix + hash
}

func stateSnapshotKey(hash string) string {
	return stateHistoryPrefix + hash
}

func syntheticLatestMeta(block Block) BlockMeta {
	cumulative := big.NewInt(0)
	if block.Header.Number > 0 {
		cumulative.SetUint64(block.Header.Number)
	}
	return BlockMeta{
		Hash:           block.Hash,
		ParentHash:     block.Header.ParentHash,
		Number:         block.Header.Number,
		Work:           "0",
		CumulativeWork: cumulative.String(),
		Canonical:      true,
	}
}

func hashKey(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

func parseBlockReference(value string, latest uint64) (uint64, error) {
	cleaned := strings.TrimSpace(strings.ToLower(value))
	if cleaned == "" || cleaned == "latest" {
		return latest, nil
	}
	if strings.HasPrefix(cleaned, "0x") {
		return strconv.ParseUint(strings.TrimPrefix(cleaned, "0x"), 16, 64)
	}
	return strconv.ParseUint(cleaned, 10, 64)
}

func inferGenesisAddress(balances map[string]*big.Int, architectAddress string) string {
	selected := ""
	var best *big.Int
	for address, amount := range balances {
		cleaned := strings.TrimSpace(address)
		if cleaned == "" || cleaned == normalizedArchitectAddress(architectAddress) {
			continue
		}
		value := cloneBigInt(amount)
		if best == nil || value.Cmp(best) > 0 || (value.Cmp(best) == 0 && cleaned < selected) {
			selected = cleaned
			best = value
		}
	}
	return selected
}

func reconcileNetworkConfig(stored, requested NetworkConfig, latest Block) (NetworkConfig, bool, error) {
	if strings.TrimSpace(stored.Name) == "" &&
		stored.ChainID == 0 &&
		strings.TrimSpace(stored.ArchitectAddress) == "" &&
		strings.TrimSpace(stored.GenesisAddress) == "" &&
		strings.TrimSpace(stored.CirculatingSupply) == "" &&
		len(stored.SystemContracts) == 0 {
		stored = requested.Clone()
		if legacyMiner := strings.TrimSpace(latest.Header.Miner); legacyMiner != "" {
			requestedArchitect := strings.TrimSpace(requested.ArchitectAddress)
			if requestedArchitect != "" &&
				requestedArchitect != constants.GenesisArchitectAddress &&
				requestedArchitect != legacyMiner {
				return NetworkConfig{}, false, fmt.Errorf("core: existing chain architect address mismatch: existing=%s requested=%s", legacyMiner, requested.ArchitectAddress)
			}
			stored.ArchitectAddress = legacyMiner
		}
		normalized, err := NormalizeNetworkConfig(stored)
		if err != nil {
			return NetworkConfig{}, false, err
		}
		return normalized, true, nil
	}

	resolved := stored.Clone()
	changed := false

	if err := ensureNetworkValueMatchWithFallback("network architect address", resolved.ArchitectAddress, requested.ArchitectAddress, constants.GenesisArchitectAddress); err != nil {
		return NetworkConfig{}, false, err
	}
	if err := ensureNetworkValueMatch("network genesis address", resolved.GenesisAddress, requested.GenesisAddress); err != nil {
		return NetworkConfig{}, false, err
	}
	if err := ensureNetworkUintMatchWithFallback("network chain ID", resolved.ChainID, requested.ChainID, constants.DefaultChainID); err != nil {
		return NetworkConfig{}, false, err
	}
	if err := ensureNetworkValueMatchWithFallback("network circulating supply", resolved.CirculatingSupply, requested.CirculatingSupply, DefaultNetworkConfig().CirculatingSupply); err != nil {
		return NetworkConfig{}, false, err
	}

	if strings.TrimSpace(resolved.Name) == "" && strings.TrimSpace(requested.Name) != "" {
		resolved.Name = requested.Name
		changed = true
	}
	if strings.TrimSpace(resolved.GenesisAddress) == "" && strings.TrimSpace(requested.GenesisAddress) != "" {
		resolved.GenesisAddress = requested.GenesisAddress
		changed = true
	}
	if strings.TrimSpace(resolved.ArchitectAddress) == "" && strings.TrimSpace(requested.ArchitectAddress) != "" {
		resolved.ArchitectAddress = requested.ArchitectAddress
		changed = true
	}
	if resolved.ChainID == 0 && requested.ChainID != 0 {
		resolved.ChainID = requested.ChainID
		changed = true
	}
	if strings.TrimSpace(resolved.CirculatingSupply) == "" && strings.TrimSpace(requested.CirculatingSupply) != "" {
		resolved.CirculatingSupply = requested.CirculatingSupply
		changed = true
	}
	if len(resolved.Bootnodes) == 0 && len(requested.Bootnodes) > 0 {
		resolved.Bootnodes = append([]string(nil), requested.Bootnodes...)
		changed = true
	}
	if len(resolved.SystemContracts) == 0 && len(requested.SystemContracts) > 0 {
		resolved.SystemContracts = cloneContractRecords(requested.SystemContracts)
		changed = true
	}

	normalized, err := NormalizeNetworkConfig(resolved)
	if err != nil {
		return NetworkConfig{}, false, err
	}
	return normalized, changed, nil
}

func ensureNetworkValueMatch(label, stored, requested string) error {
	stored = strings.TrimSpace(stored)
	requested = strings.TrimSpace(requested)
	if stored == "" || requested == "" || stored == requested {
		return nil
	}
	return fmt.Errorf("core: %s mismatch: existing=%s requested=%s", label, stored, requested)
}

func ensureNetworkValueMatchWithFallback(label, stored, requested, fallback string) error {
	requested = strings.TrimSpace(requested)
	if requested == strings.TrimSpace(fallback) {
		requested = ""
	}
	return ensureNetworkValueMatch(label, stored, requested)
}

func ensureNetworkUintMatch(label string, stored, requested uint64) error {
	if stored == 0 || requested == 0 || stored == requested {
		return nil
	}
	return fmt.Errorf("core: %s mismatch: existing=%d requested=%d", label, stored, requested)
}

func ensureNetworkUintMatchWithFallback(label string, stored, requested, fallback uint64) error {
	if requested == fallback {
		requested = 0
	}
	return ensureNetworkUintMatch(label, stored, requested)
}

func normalizedArchitectAddress(address string) string {
	cleaned := strings.TrimSpace(address)
	if cleaned == "" {
		return constants.GenesisArchitectAddress
	}
	return cleaned
}

func validateNonSystemAddress(address string) error {
	return validateNonSystemAddressWithArchitect(address, constants.GenesisArchitectAddress)
}

func validateNonSystemAddressWithArchitect(address, architectAddress string) error {
	cleaned := strings.TrimSpace(address)
	if cleaned == "" {
		return ErrInvalidTransaction
	}
	if cleaned == normalizedArchitectAddress(architectAddress) ||
		cleaned == constants.GenesisArchitectAddress ||
		cleaned == SearchEscrowAddress ||
		cleaned == constants.UNSRegistryAddress {
		return nil
	}
	_, err := types.ParseAddress(cleaned)
	return err
}

func creditBalance(balances map[string]*big.Int, address string, amount *big.Int) {
	if amount == nil || amount.Sign() == 0 {
		return
	}
	current := cloneBigInt(balances[address])
	current.Add(current, amount)
	balances[address] = current
}

func debitBalance(balances map[string]*big.Int, address string, amount *big.Int) {
	current := cloneBigInt(balances[address])
	current.Sub(current, amount)
	balances[address] = current
}

func computeTermCounts(text string) map[string]uint64 {
	counts := make(map[string]uint64)
	for _, term := range tokenize(text) {
		counts[term]++
	}
	return counts
}

func addMentionCounts(target map[string]uint64, counts map[string]uint64) {
	for term, count := range counts {
		target[term] += count
	}
}

func subtractMentionCounts(target map[string]uint64, counts map[string]uint64) {
	for term, count := range counts {
		current := target[term]
		if current <= count {
			delete(target, term)
			continue
		}
		target[term] = current - count
	}
}

func tokenize(input string) []string {
	mapped := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, input)
	return strings.Fields(mapped)
}

func normalizeTerm(term string) string {
	tokens := tokenize(term)
	if len(tokens) == 0 {
		return ""
	}
	return tokens[0]
}

func normalizeUNSName(name string) string {
	return strings.TrimSpace(name)
}

func cloneBigInt(value *big.Int) *big.Int {
	if value == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(value)
}

func cloneUint64Map(input map[string]uint64) map[string]uint64 {
	if input == nil {
		return map[string]uint64{}
	}
	out := make(map[string]uint64, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func padUint256(value *big.Int) []byte {
	bytes := value.Bytes()
	if len(bytes) >= 32 {
		return bytes[len(bytes)-32:]
	}
	padded := make([]byte, 32)
	copy(padded[32-len(bytes):], bytes)
	return padded
}

func DefaultDataDir() string {
	return filepath.Join(".", "data")
}
