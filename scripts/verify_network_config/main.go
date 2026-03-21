package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"unified/core"
)

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath        string
		rpcURL            string
		allowPlaceholders bool
	)

	flag.StringVar(&configPath, "config", "", "path to the pinned network config JSON")
	flag.StringVar(&rpcURL, "rpc-url", "", "optional node RPC URL, for example http://127.0.0.1:3337")
	flag.BoolVar(&allowPlaceholders, "allow-placeholders", false, "allow placeholder genesis/architect values in the pinned config")
	flag.Parse()

	if strings.TrimSpace(configPath) == "" {
		return fmt.Errorf("config path is required")
	}

	localConfig, err := core.LoadNetworkConfig(configPath)
	if err != nil {
		return fmt.Errorf("load pinned config: %w", err)
	}
	if !allowPlaceholders {
		if err := rejectPlaceholders(localConfig); err != nil {
			return err
		}
	}

	localHash, err := configHash(localConfig)
	if err != nil {
		return err
	}

	fmt.Printf("Pinned config: %s\n", configPath)
	fmt.Printf("Config hash: %s\n", localHash)
	fmt.Printf("Network: %s\n", localConfig.Name)
	fmt.Printf("Chain ID: %d\n", localConfig.ChainID)
	fmt.Printf("Genesis: %s\n", localConfig.GenesisAddress)
	fmt.Printf("Architect: %s\n", localConfig.ArchitectAddress)
	fmt.Printf("Bootnodes: %d\n", len(localConfig.Bootnodes))

	if strings.TrimSpace(rpcURL) == "" {
		return nil
	}

	remoteConfig, remoteChainID, err := fetchRemoteConfig(rpcURL)
	if err != nil {
		return err
	}

	mismatches := compareConfigs(localConfig, remoteConfig)
	if remoteChainID != fmt.Sprintf("0x%x", localConfig.ChainID) {
		mismatches = append(mismatches, fmt.Sprintf("chain ID RPC mismatch: expected %s got %s", fmt.Sprintf("0x%x", localConfig.ChainID), remoteChainID))
	}
	if len(mismatches) > 0 {
		fmt.Println("Remote node does not match the pinned manifest:")
		for _, mismatch := range mismatches {
			fmt.Printf("- %s\n", mismatch)
		}
		return fmt.Errorf("network config mismatch")
	}

	fmt.Printf("Remote node at %s matches the pinned manifest.\n", normalizeRPCURL(rpcURL))
	return nil
}

func rejectPlaceholders(cfg core.NetworkConfig) error {
	if containsPlaceholder(cfg.GenesisAddress) {
		return fmt.Errorf("pinned config still contains a placeholder genesis address")
	}
	if containsPlaceholder(cfg.ArchitectAddress) {
		return fmt.Errorf("pinned config still contains a placeholder architect address")
	}
	return nil
}

func containsPlaceholder(value string) bool {
	cleaned := strings.TrimSpace(value)
	return cleaned == "" || strings.Contains(cleaned, "REPLACE_ME")
}

func configHash(cfg core.NetworkConfig) (string, error) {
	normalized, err := normalizedConfigJSON(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:]), nil
}

func normalizedConfigJSON(cfg core.NetworkConfig) ([]byte, error) {
	copyCfg := normalizedForCompare(cfg)
	return json.Marshal(copyCfg)
}

func normalizedForCompare(cfg core.NetworkConfig) core.NetworkConfig {
	cloned := cfg.Clone()
	sortedBootnodes := append([]string(nil), cloned.Bootnodes...)
	sort.Strings(sortedBootnodes)
	cloned.Bootnodes = sortedBootnodes
	sort.Slice(cloned.SystemContracts, func(i, j int) bool {
		return cloned.SystemContracts[i].Address < cloned.SystemContracts[j].Address
	})
	return cloned
}

func fetchRemoteConfig(rpcURL string) (core.NetworkConfig, string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	endpoint := normalizeRPCURL(rpcURL)

	var remoteConfig core.NetworkConfig
	if err := rpcCall(client, endpoint, "ufi_getNetworkConfig", map[string]any{}, &remoteConfig); err != nil {
		return core.NetworkConfig{}, "", fmt.Errorf("fetch remote network config: %w", err)
	}
	normalized, err := core.NormalizeNetworkConfig(remoteConfig)
	if err != nil {
		return core.NetworkConfig{}, "", fmt.Errorf("normalize remote network config: %w", err)
	}

	var chainID string
	if err := rpcCall(client, endpoint, "eth_chainId", []any{}, &chainID); err != nil {
		return core.NetworkConfig{}, "", fmt.Errorf("fetch remote chain ID: %w", err)
	}
	return normalized, chainID, nil
}

func normalizeRPCURL(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if strings.HasSuffix(cleaned, "/rpc") {
		return cleaned
	}
	return strings.TrimRight(cleaned, "/") + "/rpc"
}

func rpcCall(client *http.Client, endpoint, method string, params interface{}, out interface{}) error {
	payload, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rpc status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded rpcResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	if decoded.Error != nil {
		return fmt.Errorf("rpc error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(decoded.Result, out)
}

func compareConfigs(expected, actual core.NetworkConfig) []string {
	exp := normalizedForCompare(expected)
	got := normalizedForCompare(actual)

	var mismatches []string
	if exp.Name != got.Name {
		mismatches = append(mismatches, fmt.Sprintf("network name mismatch: expected %s got %s", exp.Name, got.Name))
	}
	if exp.ChainID != got.ChainID {
		mismatches = append(mismatches, fmt.Sprintf("chain ID mismatch: expected %d got %d", exp.ChainID, got.ChainID))
	}
	if exp.GenesisAddress != got.GenesisAddress {
		mismatches = append(mismatches, fmt.Sprintf("genesis address mismatch: expected %s got %s", exp.GenesisAddress, got.GenesisAddress))
	}
	if exp.ArchitectAddress != got.ArchitectAddress {
		mismatches = append(mismatches, fmt.Sprintf("architect address mismatch: expected %s got %s", exp.ArchitectAddress, got.ArchitectAddress))
	}
	if exp.CirculatingSupply != got.CirculatingSupply {
		mismatches = append(mismatches, fmt.Sprintf("circulating supply mismatch: expected %s got %s", exp.CirculatingSupply, got.CirculatingSupply))
	}
	if !equalStringSlices(exp.Bootnodes, got.Bootnodes) {
		mismatches = append(mismatches, fmt.Sprintf("bootnodes mismatch: expected %v got %v", exp.Bootnodes, got.Bootnodes))
	}
	if !equalContracts(exp.SystemContracts, got.SystemContracts) {
		mismatches = append(mismatches, "system contract metadata mismatch")
	}
	return mismatches
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func equalContracts(left, right []core.ContractRecord) bool {
	if len(left) != len(right) {
		return false
	}
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return bytes.Equal(leftJSON, rightJSON)
}
