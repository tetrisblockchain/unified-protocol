package core

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"unified/core/consensus"
	"unified/core/constants"
	"unified/core/types"
)

func TestApplyTransactionTransferRoutesArchitectFee(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate sender key: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("derive sender address: %v", err)
	}

	recipientKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate recipient key: %v", err)
	}
	recipient, err := types.NewAddressFromPubKey(recipientKey)
	if err != nil {
		t.Fatalf("derive recipient address: %v", err)
	}

	state := NewStateSnapshot()
	state.Balances[sender.String()] = big.NewInt(10_000)

	tx := Transaction{
		Type:  TxTypeTransfer,
		From:  sender.String(),
		To:    recipient.String(),
		Value: "3000",
		Nonce: 0,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("sign tx: %v", err)
	}

	applied, err := ApplyTransaction(state, tx)
	if err != nil {
		t.Fatalf("apply tx: %v", err)
	}

	if got := state.Balances[sender.String()].String(); got != "7000" {
		t.Fatalf("sender balance = %s, want 7000", got)
	}
	if got := state.Balances[recipient.String()].String(); got != "2901" {
		t.Fatalf("recipient balance = %s, want 2901", got)
	}
	if got := state.Balances[constants.GenesisArchitectAddress].String(); got != "99" {
		t.Fatalf("architect balance = %s, want 99", got)
	}
	if state.Nonces[sender.String()] != 1 {
		t.Fatalf("nonce = %d, want 1", state.Nonces[sender.String()])
	}
	if applied.ArchitectFee.String() != "99" {
		t.Fatalf("architect fee = %s, want 99", applied.ArchitectFee.String())
	}
}

func TestApplyTransactionWithArchitectUsesConfiguredAddress(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate sender key: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("derive sender address: %v", err)
	}

	recipientKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate recipient key: %v", err)
	}
	recipient, err := types.NewAddressFromPubKey(recipientKey)
	if err != nil {
		t.Fatalf("derive recipient address: %v", err)
	}

	architectKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate architect key: %v", err)
	}
	architect, err := types.NewAddressFromPubKey(architectKey)
	if err != nil {
		t.Fatalf("derive architect address: %v", err)
	}

	state := NewStateSnapshot()
	state.Balances[sender.String()] = big.NewInt(10_000)

	tx := Transaction{
		Type:  TxTypeTransfer,
		From:  sender.String(),
		To:    recipient.String(),
		Value: "3000",
		Nonce: 0,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("sign tx: %v", err)
	}

	if _, err := ApplyTransactionWithArchitect(state, tx, architect.String()); err != nil {
		t.Fatalf("apply tx: %v", err)
	}

	if got := state.Balances[architect.String()].String(); got != "99" {
		t.Fatalf("configured architect balance = %s, want 99", got)
	}
	if got := state.Balances[constants.GenesisArchitectAddress]; got != nil {
		t.Fatalf("legacy architect placeholder unexpectedly credited: %s", got.String())
	}
}

func TestApplyTransactionRegistersUNSName(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate sender key: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("derive sender address: %v", err)
	}

	state := NewStateSnapshot()
	state.Balances[sender.String()] = big.NewInt(200_000)

	callData, err := EncodeRegisterNameCall("Architect")
	if err != nil {
		t.Fatalf("encode register call: %v", err)
	}

	tx := Transaction{
		Type:  TxTypeTransfer,
		From:  sender.String(),
		To:    constants.UNSRegistryAddress,
		Value: "100000",
		Nonce: 0,
		Data:  callData,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("sign tx: %v", err)
	}

	_, err = ApplyTransaction(state, tx)
	if err != nil {
		t.Fatalf("apply tx: %v", err)
	}

	record, ok := state.Names["Architect"]
	if !ok {
		t.Fatalf("expected Architect name record")
	}
	if record.Owner != sender.String() {
		t.Fatalf("owner = %s, want %s", record.Owner, sender.String())
	}
	if got := state.Balances[constants.UNSRegistryAddress].String(); got != "96670" {
		t.Fatalf("registry balance = %s, want 96670", got)
	}
	if got := state.Balances[constants.GenesisArchitectAddress].String(); got != "3330" {
		t.Fatalf("architect balance = %s, want 3330", got)
	}
}

func TestApplyTransactionUNSRegistrationUsesPopularityPrice(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate sender key: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("derive sender address: %v", err)
	}

	state := NewStateSnapshot()
	state.Balances[sender.String()] = big.NewInt(300_000)
	state.MentionCounts["architect"] = 2

	callData, err := EncodeRegisterNameCall("Architect")
	if err != nil {
		t.Fatalf("encode register call: %v", err)
	}

	tx := Transaction{
		Type:  TxTypeTransfer,
		From:  sender.String(),
		To:    constants.UNSRegistryAddress,
		Value: "120000",
		Nonce: 0,
		Data:  callData,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("sign tx: %v", err)
	}

	applied, err := ApplyTransaction(state, tx)
	if err != nil {
		t.Fatalf("apply tx: %v", err)
	}

	if applied.ArchitectFee.String() != "3996" {
		t.Fatalf("architect fee = %s, want 3996", applied.ArchitectFee.String())
	}
	if got := state.Balances[constants.UNSRegistryAddress].String(); got != "116004" {
		t.Fatalf("registry balance = %s, want 116004", got)
	}
}

func TestCallNativeContractReturnsUNSAndSearchQuotes(t *testing.T) {
	state := NewStateSnapshot()
	state.MentionCounts["architect"] = 3

	searchInput, err := EncodeMentionFrequencyCall("Architect")
	if err != nil {
		t.Fatalf("EncodeMentionFrequencyCall returned error: %v", err)
	}
	searchOutput, err := CallNativeContract(state, constants.SearchPrecompileAddress, searchInput)
	if err != nil {
		t.Fatalf("CallNativeContract search returned error: %v", err)
	}
	if got := new(big.Int).SetBytes(searchOutput).String(); got != "3" {
		t.Fatalf("search output = %s, want 3", got)
	}

	priceInput, err := EncodeRegistrationPriceCall("Architect")
	if err != nil {
		t.Fatalf("EncodeRegistrationPriceCall returned error: %v", err)
	}
	priceOutput, err := CallNativeContract(state, constants.UNSRegistryAddress, priceInput)
	if err != nil {
		t.Fatalf("CallNativeContract uns returned error: %v", err)
	}
	if got := new(big.Int).SetBytes(priceOutput).String(); got != "130000" {
		t.Fatalf("uns output = %s, want 130000", got)
	}
}

func TestBlockchainCallContractUsesBlockReference(t *testing.T) {
	chain, err := OpenBlockchain(BlockchainConfig{
		DataDir:         filepath.Join(t.TempDir(), "chain"),
		GenesisBalances: map[string]*big.Int{},
	})
	if err != nil {
		t.Fatalf("open blockchain: %v", err)
	}
	defer chain.Close()

	input, err := EncodeRegistrationPriceCall("Architect")
	if err != nil {
		t.Fatalf("EncodeRegistrationPriceCall returned error: %v", err)
	}
	output, err := chain.CallContract(CallMessage{
		To:   constants.UNSRegistryAddress,
		Data: input,
	}, "0x0")
	if err != nil {
		t.Fatalf("CallContract returned error: %v", err)
	}
	if got := new(big.Int).SetBytes(output).String(); got != "100000" {
		t.Fatalf("CallContract output = %s, want 100000", got)
	}
}

func TestSystemContractsExposeDescriptorMetadata(t *testing.T) {
	t.Parallel()

	search, ok := SystemContractAt(constants.SearchPrecompileAddress)
	if !ok {
		t.Fatalf("SystemContractAt(%s) not found", constants.SearchPrecompileAddress)
	}
	if !strings.HasPrefix(search.Code, "0xfe") {
		t.Fatalf("search contract code = %s, want descriptor prefix", search.Code)
	}

	uns, ok := SystemContractAt(constants.UNSRegistryAddress)
	if !ok {
		t.Fatalf("SystemContractAt(%s) not found", constants.UNSRegistryAddress)
	}
	if uns.Name != "UNSRegistry" {
		t.Fatalf("uns contract name = %s, want UNSRegistry", uns.Name)
	}
	if len(uns.Functions) < 3 {
		t.Fatalf("uns contract functions len = %d, want at least 3", len(uns.Functions))
	}
}

func TestApplyTransactionDeploysUserNoteContract(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate deployer key: %v", err)
	}
	deployer, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("derive deployer address: %v", err)
	}

	state := NewStateSnapshot()
	state.Balances[deployer.String()] = big.NewInt(10_000)

	request := ContractDeploymentRequest{
		Name:        "PublicNote",
		Runtime:     UserContractRuntimeNoteV1,
		Description: "user deployed note runtime",
		Source:      "protocol://tests/public-note",
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal deployment request: %v", err)
	}

	deployTx := Transaction{
		Type:  TxTypeContractDeploy,
		From:  deployer.String(),
		Value: "0",
		Nonce: 0,
		Data:  payload,
	}
	if err := deployTx.Sign(privateKey); err != nil {
		t.Fatalf("sign deploy tx: %v", err)
	}

	applied, err := ApplyTransaction(state, deployTx)
	if err != nil {
		t.Fatalf("apply deploy tx: %v", err)
	}
	if applied.Contract == nil {
		t.Fatalf("expected deployed contract in apply result")
	}
	record, ok := state.Contracts[applied.Contract.Address]
	if !ok {
		t.Fatalf("deployed contract not stored in state")
	}
	if record.Handler != UserContractRuntimeNoteV1 {
		t.Fatalf("runtime = %s, want %s", record.Handler, UserContractRuntimeNoteV1)
	}
	if record.Owner != deployer.String() {
		t.Fatalf("owner = %s, want %s", record.Owner, deployer.String())
	}
	if !record.Executable {
		t.Fatalf("contract should be executable")
	}
	if !strings.HasPrefix(record.Code, "0xfe") {
		t.Fatalf("descriptor code = %s, want descriptor prefix", record.Code)
	}

	setData, err := encodeSingleStringCall(setNoteSelector, "hello ledger")
	if err != nil {
		t.Fatalf("encode setNote call: %v", err)
	}
	setTx := Transaction{
		Type:  TxTypeTransfer,
		From:  deployer.String(),
		To:    record.Address,
		Value: "0",
		Nonce: 1,
		Data:  setData,
	}
	if err := setTx.Sign(privateKey); err != nil {
		t.Fatalf("sign setNote tx: %v", err)
	}
	if _, err := ApplyTransaction(state, setTx); err != nil {
		t.Fatalf("apply setNote tx: %v", err)
	}
	if got := state.ContractData[record.Address]; got != "hello ledger" {
		t.Fatalf("stored note = %q, want %q", got, "hello ledger")
	}

	output, err := ExecuteUserContractReadOnlyCall(state, record, CallMessage{
		To:   record.Address,
		Data: noteSelector[:],
	})
	if err != nil {
		t.Fatalf("read-only call returned error: %v", err)
	}
	if got := decodeABIStringForTest(t, output); got != "hello ledger" {
		t.Fatalf("note output = %q, want %q", got, "hello ledger")
	}
}

func TestBlockchainListsAndLoadsUserContracts(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate deployer key: %v", err)
	}
	deployer, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("derive deployer address: %v", err)
	}

	chain, err := OpenBlockchain(BlockchainConfig{
		DataDir: filepath.Join(t.TempDir(), "chain"),
		GenesisBalances: map[string]*big.Int{
			deployer.String(): big.NewInt(10_000),
		},
	})
	if err != nil {
		t.Fatalf("open blockchain: %v", err)
	}
	defer chain.Close()

	request := ContractDeploymentRequest{
		Name:        "PublicNote",
		Runtime:     UserContractRuntimeNoteV1,
		Description: "user deployed note runtime",
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal deployment request: %v", err)
	}

	tx := Transaction{
		Type:  TxTypeContractDeploy,
		From:  deployer.String(),
		Value: "0",
		Nonce: 0,
		Data:  payload,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("sign deploy tx: %v", err)
	}
	if _, err := chain.MineBlock(deployer.String(), []Transaction{tx}, nil); err != nil {
		t.Fatalf("MineBlock deploy returned error: %v", err)
	}

	address := deriveUserContractAddress(tx.Hash)
	record, ok := chain.ContractAt(address)
	if !ok {
		t.Fatalf("ContractAt(%s) not found", address)
	}
	if record.Name != "PublicNote" {
		t.Fatalf("contract name = %s, want PublicNote", record.Name)
	}
	if code := chain.ContractCodeAt(address); code != record.Code {
		t.Fatalf("contract code mismatch")
	}

	records := chain.ListContracts()
	found := false
	for _, candidate := range records {
		if candidate.Address == address {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("deployed contract %s not present in contract list", address)
	}
}

func decodeABIStringForTest(t *testing.T, payload []byte) string {
	t.Helper()
	if len(payload) < 64 {
		t.Fatalf("payload too short: %d", len(payload))
	}
	offset := binary.BigEndian.Uint64(payload[24:32])
	if offset != 32 {
		t.Fatalf("offset = %d, want 32", offset)
	}
	length := binary.BigEndian.Uint64(payload[56:64])
	start := 64
	end := start + int(length)
	if end > len(payload) {
		t.Fatalf("payload length %d exceeds buffer %d", end, len(payload))
	}
	return string(bytes.TrimRight(payload[start:end], "\x00"))
}

func TestMineBlockStoresSearchProofState(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate sender key: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("derive sender address: %v", err)
	}

	dataDir := filepath.Join(t.TempDir(), "chain")
	chain, err := OpenBlockchain(BlockchainConfig{
		DataDir: dataDir,
		GenesisBalances: map[string]*big.Int{
			sender.String(): big.NewInt(10_000),
		},
	})
	if err != nil {
		t.Fatalf("open blockchain: %v", err)
	}
	defer func() {
		if err := chain.Close(); err != nil {
			t.Fatalf("close blockchain: %v", err)
		}
	}()

	request := SearchTaskRequest{
		Query:           "distributed search",
		URL:             "https://example.edu",
		BaseBounty:      "100",
		Difficulty:      1,
		DataVolumeBytes: 10,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal task request: %v", err)
	}

	tx := Transaction{
		Type:  TxTypeSearchTask,
		From:  sender.String(),
		Value: "110",
		Nonce: 0,
		Data:  payload,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("sign search task tx: %v", err)
	}

	envelope, err := BuildSearchTaskEnvelope(tx, request, consensus.NewPriorityRegistry())
	if err != nil {
		t.Fatalf("build search task envelope: %v", err)
	}

	grossBounty, _ := new(big.Int).SetString(envelope.Transaction.Value, 10)
	architectFee := constants.ArchitectFee(grossBounty)
	minerReward := new(big.Int).Sub(new(big.Int).Set(grossBounty), architectFee)
	proof := CrawlProof{
		TaskID:       envelope.Transaction.Hash,
		TaskTxHash:   envelope.Transaction.Hash,
		Query:        request.Query,
		URL:          request.URL,
		Miner:        "UFI_TEST_MINER",
		Page:         consensus.IndexedPage{URL: request.URL, Title: "Example", Body: "Distributed search results", ContentHash: "abc123", SimHash: 42},
		GrossBounty:  grossBounty.String(),
		ArchitectFee: architectFee.String(),
		MinerReward:  minerReward.String(),
	}

	block, err := chain.MineBlock("UFI_TEST_MINER", []Transaction{envelope.Transaction}, []CrawlProof{proof})
	if err != nil {
		t.Fatalf("mine block: %v", err)
	}

	searchData := chain.GetSearchData(request.URL, "distributed")
	if searchData.Document == nil {
		t.Fatalf("expected indexed document")
	}
	if searchData.Document.BlockHash != block.Hash {
		t.Fatalf("document block hash = %s, want %s", searchData.Document.BlockHash, block.Hash)
	}
	if searchData.MentionFrequency == 0 {
		t.Fatalf("expected distributed term frequency to be populated")
	}
	if got := chain.GetBalance(constants.GenesisArchitectAddress).String(); got != architectFee.String() {
		t.Fatalf("architect balance = %s, want %s", got, architectFee.String())
	}

	searchResults := chain.SearchIndex("distributed", "example", 10)
	if searchResults.MentionFrequency == 0 {
		t.Fatalf("expected distributed mention frequency in ranked search")
	}
	if searchResults.Total == 0 || len(searchResults.Hits) == 0 {
		t.Fatalf("expected ranked search hits")
	}
	if searchResults.Hits[0].Document.URL != request.URL {
		t.Fatalf("top ranked URL = %s, want %s", searchResults.Hits[0].Document.URL, request.URL)
	}
}

func TestBlockchainPersistsNetworkConfigAndRejectsArchitectMismatch(t *testing.T) {
	genesisKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate genesis key: %v", err)
	}
	genesis, err := types.NewAddressFromPubKey(genesisKey)
	if err != nil {
		t.Fatalf("derive genesis address: %v", err)
	}

	architectKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate architect key: %v", err)
	}
	architect, err := types.NewAddressFromPubKey(architectKey)
	if err != nil {
		t.Fatalf("derive architect address: %v", err)
	}

	otherArchitectKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate second architect key: %v", err)
	}
	otherArchitect, err := types.NewAddressFromPubKey(otherArchitectKey)
	if err != nil {
		t.Fatalf("derive second architect address: %v", err)
	}

	dataDir := filepath.Join(t.TempDir(), "chain")
	config := NetworkConfig{
		Name:              "unified-mainnet",
		ChainID:           4444,
		GenesisAddress:    genesis.String(),
		ArchitectAddress:  architect.String(),
		CirculatingSupply: "500000",
		Bootnodes:         []string{"/ip4/66.163.125.129/tcp/4001/p2p/12D3KooWTest"},
	}

	chain, err := OpenBlockchain(BlockchainConfig{
		DataDir: dataDir,
		GenesisBalances: map[string]*big.Int{
			genesis.String(): big.NewInt(500_000),
		},
		Network: config,
	})
	if err != nil {
		t.Fatalf("open blockchain: %v", err)
	}
	if err := chain.Close(); err != nil {
		t.Fatalf("close blockchain: %v", err)
	}

	reopened, err := OpenBlockchain(BlockchainConfig{
		DataDir:         dataDir,
		GenesisBalances: map[string]*big.Int{},
		Network:         config,
	})
	if err != nil {
		t.Fatalf("reopen blockchain: %v", err)
	}

	network := reopened.NetworkConfig()
	if network.Name != config.Name || network.ChainID != config.ChainID {
		t.Fatalf("network config = %+v, want name=%s chainId=%d", network, config.Name, config.ChainID)
	}
	if network.GenesisAddress != config.GenesisAddress {
		t.Fatalf("genesis address = %s, want %s", network.GenesisAddress, config.GenesisAddress)
	}
	if network.ArchitectAddress != config.ArchitectAddress {
		t.Fatalf("architect address = %s, want %s", network.ArchitectAddress, config.ArchitectAddress)
	}
	if len(network.SystemContracts) < 2 {
		t.Fatalf("system contracts len = %d, want at least 2", len(network.SystemContracts))
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened blockchain: %v", err)
	}

	conflict := config
	conflict.ArchitectAddress = otherArchitect.String()
	if _, err := OpenBlockchain(BlockchainConfig{
		DataDir:         dataDir,
		GenesisBalances: map[string]*big.Int{},
		Network:         conflict,
	}); err == nil || !strings.Contains(err.Error(), "architect address mismatch") {
		t.Fatalf("reopen with mismatched architect returned %v, want mismatch error", err)
	}
}

func TestApplyCrawlProofSpawnsAutonomousChildTasks(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate sender key: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("derive sender address: %v", err)
	}

	chain := openTestChain(t, filepath.Join(t.TempDir(), "chain"), map[string]*big.Int{
		sender.String(): big.NewInt(10_000),
	})
	defer chain.Close()

	request := SearchTaskRequest{
		Query:           "recursive crawl",
		URL:             "https://example.com",
		BaseBounty:      "100",
		Difficulty:      1,
		DataVolumeBytes: 10,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal task request: %v", err)
	}

	tx := Transaction{
		Type:  TxTypeSearchTask,
		From:  sender.String(),
		Value: "110",
		Nonce: 0,
		Data:  payload,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("sign search task tx: %v", err)
	}

	envelope, err := BuildSearchTaskEnvelope(tx, request, consensus.NewPriorityRegistry())
	if err != nil {
		t.Fatalf("build search task envelope: %v", err)
	}

	grossBounty, _ := new(big.Int).SetString(envelope.Transaction.Value, 10)
	architectFee := constants.ArchitectFee(grossBounty)
	minerReward := new(big.Int).Sub(new(big.Int).Set(grossBounty), architectFee)
	proof := CrawlProof{
		TaskID:     envelope.Transaction.Hash,
		TaskTxHash: envelope.Transaction.Hash,
		Query:      request.Query,
		URL:        request.URL,
		Miner:      "UFI_TEST_MINER",
		Page: consensus.IndexedPage{
			URL:           request.URL,
			Title:         "Example Domain",
			Body:          "Example Domain content",
			Snippet:       "Example Domain content",
			ContentHash:   "abc123",
			SimHash:       42,
			OutboundLinks: []string{"https://example.com/docs", "https://www.iana.org/domains/example"},
		},
		GrossBounty:  grossBounty.String(),
		ArchitectFee: architectFee.String(),
		MinerReward:  minerReward.String(),
		CreatedAt:    time.Now().UTC(),
	}

	if _, err := chain.MineBlock("UFI_TEST_MINER", []Transaction{envelope.Transaction}, []CrawlProof{proof}); err != nil {
		t.Fatalf("mine block: %v", err)
	}

	snapshot, _ := chain.Snapshot()
	rootTask, ok := snapshot.PendingTasks[envelope.Transaction.Hash]
	if !ok {
		t.Fatalf("expected root task %s in state", envelope.Transaction.Hash)
	}
	if !rootTask.Completed {
		t.Fatal("expected root task to be settled")
	}

	children := make([]SearchTaskRecord, 0, 2)
	for _, task := range snapshot.PendingTasks {
		if task.ParentTaskID == rootTask.ID {
			children = append(children, task)
		}
	}
	if len(children) != 2 {
		t.Fatalf("spawned child tasks = %d, want 2", len(children))
	}

	sort.Slice(children, func(i, j int) bool { return children[i].URL < children[j].URL })
	for _, child := range children {
		if !child.Autonomous {
			t.Fatalf("child task %+v is not marked autonomous", child)
		}
		if child.Depth != 1 {
			t.Fatalf("child depth = %d, want 1", child.Depth)
		}
		if child.TxHash != envelope.Transaction.Hash {
			t.Fatalf("child tx hash = %s, want origin %s", child.TxHash, envelope.Transaction.Hash)
		}
		if child.GrossBounty != "0" || child.ArchitectFee != "0" || child.MinerReward != "0" {
			t.Fatalf("child rewards = (%s,%s,%s), want all zero", child.GrossBounty, child.ArchitectFee, child.MinerReward)
		}
	}
}

func TestBlockchainReloadPreservesLargeIndexedPages(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate sender key: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("derive sender address: %v", err)
	}

	datadir := filepath.Join(t.TempDir(), "chain")
	genesisBalances := map[string]*big.Int{
		sender.String(): big.NewInt(1_000_000),
	}

	chain := openTestChain(t, datadir, genesisBalances)

	request := SearchTaskRequest{
		Query:           "npm docs",
		URL:             "https://docs.npmjs.com/cli",
		BaseBounty:      "100",
		Difficulty:      1,
		DataVolumeBytes: 10,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	tx := Transaction{
		Type:  TxTypeSearchTask,
		From:  sender.String(),
		Value: "110",
		Nonce: 0,
		Data:  payload,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("sign search task tx: %v", err)
	}

	envelope, err := BuildSearchTaskEnvelope(tx, request, consensus.NewPriorityRegistry())
	if err != nil {
		t.Fatalf("build search task envelope: %v", err)
	}

	grossBounty, _ := new(big.Int).SetString(envelope.Transaction.Value, 10)
	architectFee := constants.ArchitectFee(grossBounty)
	minerReward := new(big.Int).Sub(new(big.Int).Set(grossBounty), architectFee)
	largeBody := strings.Repeat("npm cli docs content ", 2048)
	proof := CrawlProof{
		TaskID:     envelope.Transaction.Hash,
		TaskTxHash: envelope.Transaction.Hash,
		Query:      request.Query,
		URL:        request.URL,
		Miner:      "UFI_TEST_MINER",
		Page: consensus.IndexedPage{
			URL:         request.URL,
			Title:       "npm cli docs",
			Body:        largeBody,
			Snippet:     "npm cli docs",
			ContentHash: "npm-docs-hash",
			SimHash:     77,
		},
		GrossBounty:  grossBounty.String(),
		ArchitectFee: architectFee.String(),
		MinerReward:  minerReward.String(),
		CreatedAt:    time.Now().UTC(),
	}

	mined, err := chain.MineBlock("UFI_TEST_MINER", []Transaction{envelope.Transaction}, []CrawlProof{proof})
	if err != nil {
		t.Fatalf("mine block: %v", err)
	}
	if err := chain.Close(); err != nil {
		t.Fatalf("close chain: %v", err)
	}

	reloaded := openTestChain(t, datadir, genesisBalances)
	defer reloaded.Close()

	if reloaded.latest.Hash != mined.Hash {
		t.Fatalf("latest hash = %s, want %s", reloaded.latest.Hash, mined.Hash)
	}
	reloadedRecord, ok := reloaded.state.SearchIndex[request.URL]
	if !ok {
		t.Fatalf("expected search record for %s after reload", request.URL)
	}
	if len(reloadedRecord.Body) >= len(largeBody) {
		t.Fatalf("reloaded body length = %d, want less than %d", len(reloadedRecord.Body), len(largeBody))
	}
	if len(reloadedRecord.Body) != maxStoredProofBodyBytes {
		t.Fatalf("reloaded body length = %d, want %d", len(reloadedRecord.Body), maxStoredProofBodyBytes)
	}
	if reloadedRecord.BodyBytes != uint64(len(largeBody)) {
		t.Fatalf("reloaded body bytes = %d, want %d", reloadedRecord.BodyBytes, len(largeBody))
	}

	block, err := reloaded.GetBlockByNumber(mined.Header.Number)
	if err != nil {
		t.Fatalf("GetBlockByNumber returned error: %v", err)
	}
	if block.Hash != mined.Hash {
		t.Fatalf("block hash = %s, want %s", block.Hash, mined.Hash)
	}
	if got := len(block.Body.CrawlProofs[0].Page.Body); got != maxStoredProofBodyBytes {
		t.Fatalf("stored proof body length = %d, want %d", got, maxStoredProofBodyBytes)
	}
}
