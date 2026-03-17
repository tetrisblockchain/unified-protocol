package core

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
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

	blockNumberPrefix  = "blocks/number/"
	blockHashPrefix    = "blocks/hash/"
	blockMetaPrefix    = "blocks/meta/"
	txPrefix           = "tx/"
	balancePrefix      = "state/balance/"
	noncePrefix        = "state/nonce/"
	searchPrefix       = "state/search/"
	mentionPrefix      = "state/mention/"
	taskPrefix         = "state/task/"
	namePrefix         = "state/name/"
	stateHistoryPrefix = "state/history/"
	metaLatestHashKey  = "meta/latest_hash"
	metaLatestBlockKey = "meta/latest_number"
)

var (
	ErrBlockNotFound         = errors.New("core: block not found")
	ErrInsufficientBalance   = errors.New("core: insufficient balance")
	ErrInvalidBlock          = errors.New("core: invalid block")
	ErrInvalidSignature      = errors.New("core: invalid transaction signature")
	ErrInvalidTransaction    = errors.New("core: invalid transaction")
	ErrInvalidTransactionFee = errors.New("core: architect fee mismatch")
	ErrInvalidNonce          = errors.New("core: invalid nonce")
	ErrTaskNotFound          = errors.New("core: task not found")
	ErrTaskSettled           = errors.New("core: task already settled")
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

type BlockchainConfig struct {
	DataDir         string
	Logger          *log.Logger
	GenesisBalances map[string]*big.Int
}

type Blockchain struct {
	mu         sync.RWMutex
	db         *badger.DB
	logger     *log.Logger
	state      *StateSnapshot
	latest     Block
	latestMeta BlockMeta
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
		datadir: config.DataDir,
	}

	if err := chain.loadLocked(); err != nil {
		_ = db.Close()
		return nil, err
	}

	if chain.latest.Hash == "" {
		for address, balance := range config.GenesisBalances {
			chain.state.Balances[address] = cloneBigInt(balance)
		}
		genesis := Block{
			Header: BlockHeader{
				Number:     0,
				ParentHash: "",
				Timestamp:  time.Unix(constants.GenesisTimestampUnix, 0).UTC(),
				Miner:      constants.GenesisArchitectAddress,
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
	_, err := ApplyTransaction(snapshot, tx)
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
			return json.Unmarshal(value, &block)
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
			return json.Unmarshal(value, &block)
		})
	})
	return block, err
}

func (bc *Blockchain) HasBlockHash(hash string) bool {
	if strings.TrimSpace(hash) == "" {
		return false
	}
	_, err := bc.GetBlockByHash(hash)
	return err == nil
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
		item, err := txn.Get([]byte(stateSnapshotKey(hash)))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return ErrBlockNotFound
			}
			return err
		}
		return item.Value(func(value []byte) error {
			loaded, err := unmarshalStateSnapshot(value)
			if err != nil {
				return err
			}
			snapshot = loaded
			return nil
		})
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

	prepared, err := prepareBlock(parent, bc.state.Clone(), block)
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

	prepared, err := prepareBlock(parent, parentState, block)
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

func prepareBlock(parent Block, parentState *StateSnapshot, block Block) (preparedBlock, error) {
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
		result, err := ApplyTransaction(snapshot, tx)
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
	work := new(big.Int).SetUint64(uint64(len(transactions)))
	for _, proof := range proofs {
		record, ok := stateAfterTransactions.PendingTasks[strings.TrimSpace(proof.TaskID)]
		if !ok {
			return nil, ErrTaskNotFound
		}
		difficulty := record.Difficulty
		if difficulty == 0 {
			difficulty = 1
		}
		work.Add(work, new(big.Int).SetUint64(difficulty))
	}
	return work, nil
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

	debitBalance(state.Balances, normalized.From, value)
	creditBalance(state.Balances, constants.GenesisArchitectAddress, architectFee)
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
		if err := validateNonSystemAddress(normalized.To); err != nil {
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
			if err := json.Unmarshal(value, &record); err != nil {
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
			if err := json.Unmarshal(value, &record); err != nil {
				return err
			}
			bc.state.PendingTasks[record.ID] = record
			return nil
		}); err != nil {
			return err
		}
		if err := iteratePrefix(txn, []byte(namePrefix), func(_, value []byte) error {
			var record NameRecord
			if err := json.Unmarshal(value, &record); err != nil {
				return err
			}
			bc.state.Names[record.Name] = record
			return nil
		}); err != nil {
			return err
		}

		item, err := txn.Get([]byte(metaLatestBlockKey))
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
			return json.Unmarshal(value, &bc.latest)
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
		if err := persistState(txn, state); err != nil {
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
		if err := persistState(txn, bc.state); err != nil {
			return err
		}
		if strings.TrimSpace(bc.latest.Hash) == "" {
			return nil
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
		if err := persistState(txn, state); err != nil {
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
	blockPayload, err := json.Marshal(block)
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
	return txn.Set([]byte(stateSnapshotKey(blockHash)), payload)
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
		payload, err := json.Marshal(block)
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
		return json.Unmarshal(value, &block)
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
		payload, err := json.Marshal(record)
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
		payload, err := json.Marshal(task)
		if err != nil {
			return err
		}
		if err := txn.Set([]byte(taskPrefix+id), payload); err != nil {
			return err
		}
	}
	for name, record := range state.Names {
		payload, err := json.Marshal(record)
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
	var disk stateSnapshotDisk
	if err := json.Unmarshal(data, &disk); err != nil {
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

func validateNonSystemAddress(address string) error {
	cleaned := strings.TrimSpace(address)
	if cleaned == "" {
		return ErrInvalidTransaction
	}
	if cleaned == constants.GenesisArchitectAddress || cleaned == SearchEscrowAddress || cleaned == constants.UNSRegistryAddress {
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
