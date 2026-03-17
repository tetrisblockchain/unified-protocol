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

	chain, err := core.OpenBlockchain(core.BlockchainConfig{
		DataDir:         filepath.Join(t.TempDir(), "chain"),
		GenesisBalances: map[string]*big.Int{},
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
}
