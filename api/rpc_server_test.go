package api

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unified/core"
	"unified/core/consensus"
	coreconstants "unified/core/constants"
	"unified/core/types"
)

func TestRPCServerRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	server := NewRPCServer(nil, nil, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"ufi_getBalance","params":{"address":"` + strings.Repeat("a", MaxRPCBodyBytes) + `"}}`
	request := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(body))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestRPCServerRateLimitsMutatingMethods(t *testing.T) {
	t.Parallel()

	server := NewRPCServer(nil, nil, nil)
	server.limiter = newClientWindowLimiter(1, time.Minute)

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "ufi_sendRawTransaction",
		"params": map[string]any{
			"raw": "0xzz",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	first := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(body))
	first.RemoteAddr = "127.0.0.1:12345"
	firstResponse := httptest.NewRecorder()
	server.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusBadRequest {
		t.Fatalf("first status = %d, want %d", firstResponse.Code, http.StatusBadRequest)
	}

	second := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(body))
	second.RemoteAddr = "127.0.0.1:12345"
	secondResponse := httptest.NewRecorder()
	server.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", secondResponse.Code, http.StatusTooManyRequests)
	}
}

func TestRPCServerGetNamePriceAndCallNative(t *testing.T) {
	t.Parallel()

	chain, err := core.OpenBlockchain(core.BlockchainConfig{
		DataDir:         filepath.Join(t.TempDir(), "chain"),
		GenesisBalances: map[string]*big.Int{},
	})
	if err != nil {
		t.Fatalf("OpenBlockchain returned error: %v", err)
	}
	defer chain.Close()

	server := NewRPCServer(chain, nil, nil)

	namePriceBody := bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"ufi_getNamePrice","params":{"name":"Architect"}}`))
	namePriceRequest := httptest.NewRequest(http.MethodPost, "/rpc", namePriceBody)
	namePriceResponse := httptest.NewRecorder()
	server.ServeHTTP(namePriceResponse, namePriceRequest)
	if namePriceResponse.Code != http.StatusOK {
		t.Fatalf("ufi_getNamePrice status = %d, want %d", namePriceResponse.Code, http.StatusOK)
	}
	if !strings.Contains(namePriceResponse.Body.String(), `"price":"100000"`) {
		t.Fatalf("ufi_getNamePrice body = %s, want quoted base price", namePriceResponse.Body.String())
	}

	input, err := core.EncodeMentionFrequencyCall("Architect")
	if err != nil {
		t.Fatalf("EncodeMentionFrequencyCall returned error: %v", err)
	}
	callNativeBody := bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":2,"method":"ufi_callNative","params":{"to":"0x101","data":"0x` + strings.ToLower(fmt.Sprintf("%x", input)) + `"}}`))
	callNativeRequest := httptest.NewRequest(http.MethodPost, "/rpc", callNativeBody)
	callNativeResponse := httptest.NewRecorder()
	server.ServeHTTP(callNativeResponse, callNativeRequest)
	if callNativeResponse.Code != http.StatusOK {
		t.Fatalf("ufi_callNative status = %d, want %d", callNativeResponse.Code, http.StatusOK)
	}
	if !strings.Contains(callNativeResponse.Body.String(), `"output":"0x`) {
		t.Fatalf("ufi_callNative body = %s, want output payload", callNativeResponse.Body.String())
	}
}

func TestRPCServerEthCallSupportsArrayParams(t *testing.T) {
	t.Parallel()

	chain, err := core.OpenBlockchain(core.BlockchainConfig{
		DataDir:         filepath.Join(t.TempDir(), "chain"),
		GenesisBalances: map[string]*big.Int{},
	})
	if err != nil {
		t.Fatalf("OpenBlockchain returned error: %v", err)
	}
	defer chain.Close()

	server := NewRPCServer(chain, nil, nil)
	input, err := core.EncodeRegistrationPriceCall("Architect")
	if err != nil {
		t.Fatalf("EncodeRegistrationPriceCall returned error: %v", err)
	}

	payload := `{"jsonrpc":"2.0","id":3,"method":"eth_call","params":[{"to":"0x102","data":"0x` + strings.ToLower(fmt.Sprintf("%x", input)) + `"},"0x0"]}`
	request := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(payload))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("eth_call status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"result":"0x`) {
		t.Fatalf("eth_call body = %s, want hex result", response.Body.String())
	}
}

func TestRPCServerCallRejectsNonZeroValue(t *testing.T) {
	t.Parallel()

	chain, err := core.OpenBlockchain(core.BlockchainConfig{
		DataDir:         filepath.Join(t.TempDir(), "chain"),
		GenesisBalances: map[string]*big.Int{},
	})
	if err != nil {
		t.Fatalf("OpenBlockchain returned error: %v", err)
	}
	defer chain.Close()

	server := NewRPCServer(chain, nil, nil)
	input, err := core.EncodeMentionFrequencyCall("Architect")
	if err != nil {
		t.Fatalf("EncodeMentionFrequencyCall returned error: %v", err)
	}

	payload := `{"jsonrpc":"2.0","id":4,"method":"ufi_call","params":{"to":"0x101","data":"0x` + strings.ToLower(fmt.Sprintf("%x", input)) + `","value":"1"}}`
	request := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(payload))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("ufi_call status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if !strings.Contains(response.Body.String(), "does not accept value") {
		t.Fatalf("ufi_call body = %s, want non-payable error", response.Body.String())
	}
}

func TestRPCServerGetTransactionCountReportsLatestAndPending(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("NewAddressFromPubKey returned error: %v", err)
	}

	chain, err := core.OpenBlockchain(core.BlockchainConfig{
		DataDir: filepath.Join(t.TempDir(), "chain"),
		GenesisBalances: map[string]*big.Int{
			sender.String(): big.NewInt(1_000_000),
		},
	})
	if err != nil {
		t.Fatalf("OpenBlockchain returned error: %v", err)
	}
	defer chain.Close()

	engine := core.NewEngine(chain, consensus.Miner{PriorityRegistry: consensus.NewPriorityRegistry()}, "UFI_TEST_MINER", nil)
	request := core.SearchTaskRequest{
		Query:           "initial web seed",
		URL:             "https://example.com",
		BaseBounty:      "100",
		Difficulty:      1,
		DataVolumeBytes: 10,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	totalValue, err := consensus.QuoteBounty(big.NewInt(100), 1, 10)
	if err != nil {
		t.Fatalf("QuoteBounty returned error: %v", err)
	}
	tx := core.Transaction{
		Type:  core.TxTypeSearchTask,
		From:  sender.String(),
		Value: totalValue.String(),
		Nonce: 0,
		Data:  payload,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	if _, err := engine.SubmitSearchTask(tx, request); err != nil {
		t.Fatalf("SubmitSearchTask returned error: %v", err)
	}

	server := NewRPCServer(chain, engine, nil)

	latestPayload := `{"jsonrpc":"2.0","id":5,"method":"ufi_getTransactionCount","params":{"address":"` + sender.String() + `","block":"latest"}}`
	latestRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(latestPayload))
	latestResponse := httptest.NewRecorder()
	server.ServeHTTP(latestResponse, latestRequest)
	if latestResponse.Code != http.StatusOK {
		t.Fatalf("ufi_getTransactionCount status = %d, want %d", latestResponse.Code, http.StatusOK)
	}
	if !strings.Contains(latestResponse.Body.String(), `"nonce":"0"`) {
		t.Fatalf("ufi_getTransactionCount body = %s, want latest nonce 0", latestResponse.Body.String())
	}

	pendingPayload := `{"jsonrpc":"2.0","id":6,"method":"eth_getTransactionCount","params":["` + sender.String() + `","pending"]}`
	pendingRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(pendingPayload))
	pendingResponse := httptest.NewRecorder()
	server.ServeHTTP(pendingResponse, pendingRequest)
	if pendingResponse.Code != http.StatusOK {
		t.Fatalf("eth_getTransactionCount status = %d, want %d", pendingResponse.Code, http.StatusOK)
	}
	if !strings.Contains(pendingResponse.Body.String(), `"result":"0x1"`) {
		t.Fatalf("eth_getTransactionCount body = %s, want pending nonce 0x1", pendingResponse.Body.String())
	}
}

func TestRPCServerGetCodeAndContracts(t *testing.T) {
	t.Parallel()

	architectKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	architect, err := types.NewAddressFromPubKey(architectKey)
	if err != nil {
		t.Fatalf("NewAddressFromPubKey returned error: %v", err)
	}

	chain, err := core.OpenBlockchain(core.BlockchainConfig{
		DataDir:         filepath.Join(t.TempDir(), "chain"),
		GenesisBalances: map[string]*big.Int{},
		Network: core.NetworkConfig{
			Name:             "unified-mainnet",
			ChainID:          4444,
			ArchitectAddress: architect.String(),
		},
	})
	if err != nil {
		t.Fatalf("OpenBlockchain returned error: %v", err)
	}
	defer chain.Close()

	server := NewRPCServer(chain, nil, nil)

	getCodePayload := `{"jsonrpc":"2.0","id":7,"method":"eth_getCode","params":["0x102","latest"]}`
	getCodeRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(getCodePayload))
	getCodeResponse := httptest.NewRecorder()
	server.ServeHTTP(getCodeResponse, getCodeRequest)
	if getCodeResponse.Code != http.StatusOK {
		t.Fatalf("eth_getCode status = %d, want %d", getCodeResponse.Code, http.StatusOK)
	}
	if !strings.Contains(getCodeResponse.Body.String(), `"result":"0xfe`) {
		t.Fatalf("eth_getCode body = %s, want descriptor bytecode", getCodeResponse.Body.String())
	}

	getContractPayload := `{"jsonrpc":"2.0","id":8,"method":"ufi_getContract","params":{"address":"0x101"}}`
	getContractRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(getContractPayload))
	getContractResponse := httptest.NewRecorder()
	server.ServeHTTP(getContractResponse, getContractRequest)
	if getContractResponse.Code != http.StatusOK {
		t.Fatalf("ufi_getContract status = %d, want %d", getContractResponse.Code, http.StatusOK)
	}
	if !strings.Contains(getContractResponse.Body.String(), `"address":"0x101"`) || !strings.Contains(getContractResponse.Body.String(), `"name":"SearchPrecompile"`) {
		t.Fatalf("ufi_getContract body = %s, want search precompile record", getContractResponse.Body.String())
	}

	listContractsPayload := `{"jsonrpc":"2.0","id":9,"method":"ufi_listContracts","params":{}}`
	listContractsRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(listContractsPayload))
	listContractsResponse := httptest.NewRecorder()
	server.ServeHTTP(listContractsResponse, listContractsRequest)
	if listContractsResponse.Code != http.StatusOK {
		t.Fatalf("ufi_listContracts status = %d, want %d", listContractsResponse.Code, http.StatusOK)
	}
	if !strings.Contains(listContractsResponse.Body.String(), `"address":"0x101"`) || !strings.Contains(listContractsResponse.Body.String(), `"address":"0x102"`) {
		t.Fatalf("ufi_listContracts body = %s, want both system contracts", listContractsResponse.Body.String())
	}

	networkPayload := `{"jsonrpc":"2.0","id":12,"method":"ufi_getNetworkConfig","params":{}}`
	networkRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(networkPayload))
	networkResponse := httptest.NewRecorder()
	server.ServeHTTP(networkResponse, networkRequest)
	if networkResponse.Code != http.StatusOK {
		t.Fatalf("ufi_getNetworkConfig status = %d, want %d", networkResponse.Code, http.StatusOK)
	}
	if !strings.Contains(networkResponse.Body.String(), `"name":"unified-mainnet"`) || !strings.Contains(networkResponse.Body.String(), `"architectAddress":"`+architect.String()+`"`) {
		t.Fatalf("ufi_getNetworkConfig body = %s, want network metadata", networkResponse.Body.String())
	}

	chainIDPayload := `{"jsonrpc":"2.0","id":13,"method":"eth_chainId","params":[]}`
	chainIDRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(chainIDPayload))
	chainIDResponse := httptest.NewRecorder()
	server.ServeHTTP(chainIDResponse, chainIDRequest)
	if chainIDResponse.Code != http.StatusOK {
		t.Fatalf("eth_chainId status = %d, want %d", chainIDResponse.Code, http.StatusOK)
	}
	if !strings.Contains(chainIDResponse.Body.String(), `"result":"0x115c"`) {
		t.Fatalf("eth_chainId body = %s, want 0x115c", chainIDResponse.Body.String())
	}
}

func TestRPCServerResolveAndReverseResolve(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		t.Fatalf("NewAddressFromPubKey returned error: %v", err)
	}
	callData, err := core.EncodeRegisterNameCall("Architect")
	if err != nil {
		t.Fatalf("EncodeRegisterNameCall returned error: %v", err)
	}

	chain, err := core.OpenBlockchain(core.BlockchainConfig{
		DataDir: filepath.Join(t.TempDir(), "chain"),
		GenesisBalances: map[string]*big.Int{
			sender.String(): big.NewInt(200_000),
		},
	})
	if err != nil {
		t.Fatalf("OpenBlockchain returned error: %v", err)
	}
	defer chain.Close()

	tx := core.Transaction{
		Type:  core.TxTypeTransfer,
		From:  sender.String(),
		To:    coreconstants.UNSRegistryAddress,
		Value: "100000",
		Nonce: 0,
		Data:  callData,
	}
	if err := tx.Sign(privateKey); err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	if _, err := chain.MineBlock("UFI_TEST_MINER", []core.Transaction{tx}, nil); err != nil {
		t.Fatalf("MineBlock returned error: %v", err)
	}

	server := NewRPCServer(chain, nil, nil)

	resolvePayload := `{"jsonrpc":"2.0","id":10,"method":"ufi_resolveName","params":{"name":"Architect"}}`
	resolveRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(resolvePayload))
	resolveResponse := httptest.NewRecorder()
	server.ServeHTTP(resolveResponse, resolveRequest)
	if resolveResponse.Code != http.StatusOK {
		t.Fatalf("ufi_resolveName status = %d, want %d", resolveResponse.Code, http.StatusOK)
	}
	if !strings.Contains(resolveResponse.Body.String(), `"found":true`) || !strings.Contains(resolveResponse.Body.String(), sender.String()) {
		t.Fatalf("ufi_resolveName body = %s, want owner match", resolveResponse.Body.String())
	}

	reversePayload := `{"jsonrpc":"2.0","id":11,"method":"ufi_reverseResolve","params":{"address":"` + sender.String() + `"}}`
	reverseRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(reversePayload))
	reverseResponse := httptest.NewRecorder()
	server.ServeHTTP(reverseResponse, reverseRequest)
	if reverseResponse.Code != http.StatusOK {
		t.Fatalf("ufi_reverseResolve status = %d, want %d", reverseResponse.Code, http.StatusOK)
	}
	if !strings.Contains(reverseResponse.Body.String(), `"name":"Architect"`) {
		t.Fatalf("ufi_reverseResolve body = %s, want Architect", reverseResponse.Body.String())
	}
}

func TestRPCServerActivityEndpoints(t *testing.T) {
	t.Parallel()

	senderPublicKey, senderPrivateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey sender returned error: %v", err)
	}
	sender, err := types.NewAddressFromPubKey(senderPublicKey)
	if err != nil {
		t.Fatalf("NewAddressFromPubKey sender returned error: %v", err)
	}

	recipientPublicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey recipient returned error: %v", err)
	}
	recipient, err := types.NewAddressFromPubKey(recipientPublicKey)
	if err != nil {
		t.Fatalf("NewAddressFromPubKey recipient returned error: %v", err)
	}

	chain, err := core.OpenBlockchain(core.BlockchainConfig{
		DataDir: filepath.Join(t.TempDir(), "chain"),
		GenesisBalances: map[string]*big.Int{
			sender.String(): big.NewInt(1_000_000),
		},
	})
	if err != nil {
		t.Fatalf("OpenBlockchain returned error: %v", err)
	}
	defer chain.Close()

	transfer := core.Transaction{
		Type:      core.TxTypeTransfer,
		From:      sender.String(),
		To:        recipient.String(),
		Value:     "25000",
		Nonce:     0,
		Timestamp: time.Unix(1_700_000_000, 0).UTC(),
	}
	if err := transfer.Sign(senderPrivateKey); err != nil {
		t.Fatalf("Sign transfer returned error: %v", err)
	}
	if _, err := chain.MineBlock("UFI_TEST_MINER", []core.Transaction{transfer}, nil); err != nil {
		t.Fatalf("MineBlock transfer returned error: %v", err)
	}

	request := core.SearchTaskRequest{
		Query:           "distributed search",
		URL:             "https://example.edu",
		BaseBounty:      "100",
		Difficulty:      1,
		DataVolumeBytes: 10,
	}
	requestPayload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal search request returned error: %v", err)
	}

	searchTask := core.Transaction{
		Type:      core.TxTypeSearchTask,
		From:      sender.String(),
		Value:     "110",
		Nonce:     1,
		Timestamp: time.Unix(1_700_000_060, 0).UTC(),
		Data:      requestPayload,
	}
	if err := searchTask.Sign(senderPrivateKey); err != nil {
		t.Fatalf("Sign search task returned error: %v", err)
	}

	envelope, err := core.BuildSearchTaskEnvelope(searchTask, request, consensus.NewPriorityRegistry())
	if err != nil {
		t.Fatalf("BuildSearchTaskEnvelope returned error: %v", err)
	}

	grossBounty, _ := new(big.Int).SetString(envelope.Transaction.Value, 10)
	architectFee := coreconstants.ArchitectFee(grossBounty)
	minerReward := new(big.Int).Sub(new(big.Int).Set(grossBounty), architectFee)
	proof := core.CrawlProof{
		TaskID:       envelope.Transaction.Hash,
		TaskTxHash:   envelope.Transaction.Hash,
		Query:        request.Query,
		URL:          request.URL,
		Miner:        "UFI_TEST_MINER",
		GrossBounty:  grossBounty.String(),
		ArchitectFee: architectFee.String(),
		MinerReward:  minerReward.String(),
		Page: consensus.IndexedPage{
			URL:         request.URL,
			Title:       "Example EDU",
			Snippet:     "Distributed search result",
			Body:        "Distributed search results for EDU domains.",
			IndexedAt:   time.Unix(1_700_000_060, 0).UTC(),
			ContentHash: "content-123",
			SimHash:     42,
		},
	}
	if _, err := chain.MineBlock("UFI_TEST_MINER", []core.Transaction{envelope.Transaction}, []core.CrawlProof{proof}); err != nil {
		t.Fatalf("MineBlock search task returned error: %v", err)
	}

	server := NewRPCServer(chain, nil, nil)

	recentPayload := `{"jsonrpc":"2.0","id":20,"method":"ufi_getRecentActivity","params":{"limit":4}}`
	recentRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(recentPayload))
	recentResponse := httptest.NewRecorder()
	server.ServeHTTP(recentResponse, recentRequest)
	if recentResponse.Code != http.StatusOK {
		t.Fatalf("ufi_getRecentActivity status = %d, want %d", recentResponse.Code, http.StatusOK)
	}
	if !strings.Contains(recentResponse.Body.String(), envelope.Transaction.Hash) || !strings.Contains(recentResponse.Body.String(), `"proof":{"taskId":"`+envelope.Transaction.Hash) {
		t.Fatalf("ufi_getRecentActivity body = %s, want tx and proof activity", recentResponse.Body.String())
	}

	addressPayload := `{"jsonrpc":"2.0","id":21,"method":"ufi_getAddressActivity","params":{"address":"` + sender.String() + `","limit":6}}`
	addressRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(addressPayload))
	addressResponse := httptest.NewRecorder()
	server.ServeHTTP(addressResponse, addressRequest)
	if addressResponse.Code != http.StatusOK {
		t.Fatalf("ufi_getAddressActivity status = %d, want %d", addressResponse.Code, http.StatusOK)
	}
	if !strings.Contains(addressResponse.Body.String(), transfer.Hash) || !strings.Contains(addressResponse.Body.String(), envelope.Transaction.Hash) || !strings.Contains(addressResponse.Body.String(), `"taskTxHash":"`+envelope.Transaction.Hash) {
		t.Fatalf("ufi_getAddressActivity body = %s, want transfer, task, and proof", addressResponse.Body.String())
	}

	txPayload := `{"jsonrpc":"2.0","id":22,"method":"ufi_getTransactionByHash","params":{"hash":"` + envelope.Transaction.Hash + `"}}`
	txRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(txPayload))
	txResponse := httptest.NewRecorder()
	server.ServeHTTP(txResponse, txRequest)
	if txResponse.Code != http.StatusOK {
		t.Fatalf("ufi_getTransactionByHash status = %d, want %d", txResponse.Code, http.StatusOK)
	}
	if !strings.Contains(txResponse.Body.String(), `"blockNumber":2`) || !strings.Contains(txResponse.Body.String(), envelope.Transaction.Hash) {
		t.Fatalf("ufi_getTransactionByHash body = %s, want block number and hash", txResponse.Body.String())
	}

	proofPayload := `{"jsonrpc":"2.0","id":23,"method":"ufi_getCrawlProof","params":{"taskTxHash":"` + envelope.Transaction.Hash + `"}}`
	proofRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(proofPayload))
	proofResponse := httptest.NewRecorder()
	server.ServeHTTP(proofResponse, proofRequest)
	if proofResponse.Code != http.StatusOK {
		t.Fatalf("ufi_getCrawlProof status = %d, want %d", proofResponse.Code, http.StatusOK)
	}
	if !strings.Contains(proofResponse.Body.String(), `"url":"https://example.edu"`) || !strings.Contains(proofResponse.Body.String(), `"blockNumber":2`) {
		t.Fatalf("ufi_getCrawlProof body = %s, want URL and block number", proofResponse.Body.String())
	}

	blockPayload := `{"jsonrpc":"2.0","id":24,"method":"ufi_getBlockByHash","params":{"hash":"` + chain.LatestBlock().Hash + `"}}`
	blockRequest := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(blockPayload))
	blockResponse := httptest.NewRecorder()
	server.ServeHTTP(blockResponse, blockRequest)
	if blockResponse.Code != http.StatusOK {
		t.Fatalf("ufi_getBlockByHash status = %d, want %d", blockResponse.Code, http.StatusOK)
	}
	if !strings.Contains(blockResponse.Body.String(), `"hash":"`+chain.LatestBlock().Hash+`"`) {
		t.Fatalf("ufi_getBlockByHash body = %s, want latest block hash", blockResponse.Body.String())
	}
}
