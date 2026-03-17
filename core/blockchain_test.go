package core

import (
	"crypto/ed25519"
	"encoding/json"
	"math/big"
	"path/filepath"
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
