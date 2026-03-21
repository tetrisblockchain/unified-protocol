package core

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"math/big"
	"path/filepath"
	"strconv"
	"testing"

	"unified/core/consensus"
	"unified/core/types"
)

func TestImportBlockReorgsToHeavierUsefulWorkBranch(t *testing.T) {
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

	genesisBalances := map[string]*big.Int{
		sender.String(): big.NewInt(1_000_000),
	}
	sourceA := openTestChain(t, filepath.Join(t.TempDir(), "source-a"), genesisBalances)
	defer sourceA.Close()
	remote := openTestChain(t, filepath.Join(t.TempDir(), "remote"), genesisBalances)
	defer remote.Close()

	blockA1 := mineTransferBlock(t, sourceA, privateKey, sender.String(), recipient.String(), 1000, 0)
	blockA2 := mineTransferBlock(t, sourceA, privateKey, sender.String(), recipient.String(), 1000, 1)
	if err := remote.ImportBlock(blockA1); err != nil {
		t.Fatalf("import blockA1: %v", err)
	}
	if err := remote.ImportBlock(blockA2); err != nil {
		t.Fatalf("import blockA2: %v", err)
	}

	sourceB := openTestChain(t, filepath.Join(t.TempDir(), "source-b"), genesisBalances)
	defer sourceB.Close()
	request := SearchTaskRequest{
		Query:           "distributed search",
		URL:             "https://reorg.example.edu",
		BaseBounty:      "100",
		Difficulty:      5,
		DataVolumeBytes: 10,
	}
	blockB1 := mineSearchTaskBlock(t, sourceB, privateKey, sender.String(), 0, request, "UFI_REORG_MINER")
	if err := remote.ImportBlock(blockB1); err != nil {
		t.Fatalf("import blockB1: %v", err)
	}

	if got := remote.LatestBlock().Hash; got != blockB1.Hash {
		t.Fatalf("latest hash = %s, want %s", got, blockB1.Hash)
	}
	if got := remote.LatestBlock().Header.Number; got != 1 {
		t.Fatalf("latest number = %d, want 1", got)
	}
	if got := remote.GetBalance(recipient.String()).Sign(); got != 0 {
		t.Fatalf("recipient balance sign = %d, want 0 after reorg", got)
	}

	searchData := remote.GetSearchData(request.URL, "distributed")
	if searchData.Document == nil {
		t.Fatalf("expected crawled search document after reorg")
	}
	if searchData.Document.BlockHash != blockB1.Hash {
		t.Fatalf("document block hash = %s, want %s", searchData.Document.BlockHash, blockB1.Hash)
	}

	blockOne, err := remote.GetBlockByNumber(1)
	if err != nil {
		t.Fatalf("GetBlockByNumber(1) returned error: %v", err)
	}
	if blockOne.Hash != blockB1.Hash {
		t.Fatalf("canonical block #1 = %s, want %s", blockOne.Hash, blockB1.Hash)
	}
	if _, err := remote.GetBlockByNumber(2); !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("GetBlockByNumber(2) error = %v, want ErrBlockNotFound", err)
	}
}

func TestSideBranchPersistsAcrossRestartAndCanReorg(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate sender key: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("derive sender address: %v", err)
	}

	recipientAKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate recipientA key: %v", err)
	}
	recipientA, err := types.NewAddressFromPubKey(recipientAKey)
	if err != nil {
		t.Fatalf("derive recipientA address: %v", err)
	}

	recipientBKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate recipientB key: %v", err)
	}
	recipientB, err := types.NewAddressFromPubKey(recipientBKey)
	if err != nil {
		t.Fatalf("derive recipientB address: %v", err)
	}

	genesisBalances := map[string]*big.Int{
		sender.String(): big.NewInt(1_000_000),
	}
	sourceA := openTestChain(t, filepath.Join(t.TempDir(), "source-a"), genesisBalances)
	defer sourceA.Close()
	sourceB := openTestChain(t, filepath.Join(t.TempDir(), "source-b"), genesisBalances)
	defer sourceB.Close()

	blockA1 := mineTransferBlock(t, sourceA, privateKey, sender.String(), recipientA.String(), 1000, 0)
	blockA2 := mineTransferBlock(t, sourceA, privateKey, sender.String(), recipientA.String(), 1000, 1)
	blockB1 := mineTransferBlock(t, sourceB, privateKey, sender.String(), recipientB.String(), 1000, 0)
	blockB2 := mineSearchTaskBlock(t, sourceB, privateKey, sender.String(), 1, SearchTaskRequest{
		Query:           "persistent side branch",
		URL:             "https://branch.example.edu",
		BaseBounty:      "100",
		Difficulty:      5,
		DataVolumeBytes: 10,
	}, "UFI_SIDE_BRANCH_MINER")

	datadir := filepath.Join(t.TempDir(), "remote")
	remote := openTestChain(t, datadir, genesisBalances)
	if err := remote.ImportBlock(blockA1); err != nil {
		t.Fatalf("import blockA1: %v", err)
	}
	if err := remote.ImportBlock(blockA2); err != nil {
		t.Fatalf("import blockA2: %v", err)
	}
	if err := remote.ImportBlock(blockB1); err != nil {
		t.Fatalf("import blockB1: %v", err)
	}
	if got := remote.LatestBlock().Hash; got != blockA2.Hash {
		t.Fatalf("latest hash after lighter side branch = %s, want %s", got, blockA2.Hash)
	}
	if !remote.HasBlockHash(blockB1.Hash) {
		t.Fatalf("expected side-branch block %s to be stored", blockB1.Hash)
	}
	if err := remote.Close(); err != nil {
		t.Fatalf("close remote: %v", err)
	}

	reloaded := openTestChain(t, datadir, genesisBalances)
	defer reloaded.Close()
	if !reloaded.HasBlockHash(blockB1.Hash) {
		t.Fatalf("expected persisted side-branch block %s after restart", blockB1.Hash)
	}
	if err := reloaded.ImportBlock(blockB2); err != nil {
		t.Fatalf("import blockB2: %v", err)
	}

	if got := reloaded.LatestBlock().Hash; got != blockB2.Hash {
		t.Fatalf("latest hash after restart reorg = %s, want %s", got, blockB2.Hash)
	}
	if got := reloaded.GetBalance(recipientA.String()).Sign(); got != 0 {
		t.Fatalf("recipientA balance sign = %d, want 0 after reorg", got)
	}
	if got := reloaded.GetBalance(recipientB.String()).String(); got != "967" {
		t.Fatalf("recipientB balance = %s, want 967", got)
	}
	searchData := reloaded.GetSearchData("https://branch.example.edu", "persistent")
	if searchData.Document == nil {
		t.Fatalf("expected search data on reorged branch")
	}

	blockOne, err := reloaded.GetBlockByNumber(1)
	if err != nil {
		t.Fatalf("GetBlockByNumber(1): %v", err)
	}
	if blockOne.Hash != blockB1.Hash {
		t.Fatalf("canonical block #1 = %s, want %s", blockOne.Hash, blockB1.Hash)
	}
	blockTwo, err := reloaded.GetBlockByNumber(2)
	if err != nil {
		t.Fatalf("GetBlockByNumber(2): %v", err)
	}
	if blockTwo.Hash != blockB2.Hash {
		t.Fatalf("canonical block #2 = %s, want %s", blockTwo.Hash, blockB2.Hash)
	}
}

func openTestChain(t *testing.T, datadir string, genesisBalances map[string]*big.Int) *Blockchain {
	t.Helper()

	chain, err := OpenBlockchain(BlockchainConfig{
		DataDir:         datadir,
		GenesisBalances: genesisBalances,
	})
	if err != nil {
		t.Fatalf("OpenBlockchain(%s) returned error: %v", datadir, err)
	}
	return chain
}

func mineTransferBlock(t *testing.T, chain *Blockchain, privateKey ed25519.PrivateKey, from, to string, value int64, nonce uint64) Block {
	t.Helper()

	tx := Transaction{
		Type:  TxTypeTransfer,
		From:  from,
		To:    to,
		Value: strconv.FormatInt(value, 10),
		Nonce: nonce,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("sign transfer tx: %v", err)
	}

	block, err := chain.MineBlock(from, []Transaction{tx}, nil)
	if err != nil {
		t.Fatalf("MineBlock transfer returned error: %v", err)
	}
	return block
}

func mineSearchTaskBlock(t *testing.T, chain *Blockchain, privateKey ed25519.PrivateKey, from string, nonce uint64, request SearchTaskRequest, miner string) Block {
	t.Helper()

	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal search task request: %v", err)
	}
	baseBounty, ok := new(big.Int).SetString(request.BaseBounty, 10)
	if !ok {
		t.Fatalf("invalid base bounty %q", request.BaseBounty)
	}
	totalBounty, err := consensus.QuoteBounty(baseBounty, request.Difficulty, request.DataVolumeBytes)
	if err != nil {
		t.Fatalf("QuoteBounty returned error: %v", err)
	}

	tx := Transaction{
		Type:  TxTypeSearchTask,
		From:  from,
		Value: totalBounty.String(),
		Nonce: nonce,
		Data:  payload,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("sign search task tx: %v", err)
	}

	envelope, err := BuildSearchTaskEnvelope(tx, request, consensus.NewPriorityRegistry())
	if err != nil {
		t.Fatalf("BuildSearchTaskEnvelope returned error: %v", err)
	}
	proof := CrawlProof{
		TaskID:       envelope.Transaction.Hash,
		TaskTxHash:   envelope.Transaction.Hash,
		Query:        request.Query,
		URL:          request.URL,
		Miner:        miner,
		Page:         consensus.IndexedPage{URL: request.URL, Title: "Example", Body: request.Query + " body", Snippet: request.Query + " body", ContentHash: "abc123", SimHash: consensus.SimHash(request.Query + " body")},
		GrossBounty:  envelope.Transaction.Value,
		ArchitectFee: envelope.Task.ArchitectFee.String(),
		MinerReward:  envelope.Task.MinerReward.String(),
	}

	block, err := chain.MineBlock(miner, []Transaction{envelope.Transaction}, []CrawlProof{proof})
	if err != nil {
		t.Fatalf("MineBlock search task returned error: %v", err)
	}
	return block
}
