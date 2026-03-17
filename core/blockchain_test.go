package core

import (
	"crypto/ed25519"
	"encoding/json"
	"math/big"
	"path/filepath"
	"strings"
	"testing"

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
}
