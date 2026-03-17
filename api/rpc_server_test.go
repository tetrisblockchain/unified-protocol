package api

import (
	"bytes"
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
