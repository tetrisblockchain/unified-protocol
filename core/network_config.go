package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"unified/core/constants"
	"unified/core/types"
)

type NetworkConfig struct {
	Name              string           `json:"name"`
	ChainID           uint64           `json:"chainId"`
	GenesisAddress    string           `json:"genesisAddress"`
	ArchitectAddress  string           `json:"architectAddress"`
	CirculatingSupply string           `json:"circulatingSupply"`
	Bootnodes         []string         `json:"bootnodes,omitempty"`
	SystemContracts   []ContractRecord `json:"systemContracts,omitempty"`
}

func DefaultNetworkConfig() NetworkConfig {
	return NetworkConfig{
		Name:              "unified-local",
		ChainID:           constants.DefaultChainID,
		ArchitectAddress:  strings.TrimSpace(constants.GenesisArchitectAddress),
		CirculatingSupply: "1000000",
		SystemContracts:   ListSystemContracts(),
	}
}

func (cfg NetworkConfig) Clone() NetworkConfig {
	cloned := cfg
	cloned.Bootnodes = append([]string(nil), cfg.Bootnodes...)
	cloned.SystemContracts = cloneContractRecords(cfg.SystemContracts)
	return cloned
}

func NormalizeNetworkConfig(cfg NetworkConfig) (NetworkConfig, error) {
	normalized := DefaultNetworkConfig()

	if name := strings.TrimSpace(cfg.Name); name != "" {
		normalized.Name = name
	}
	if cfg.ChainID != 0 {
		normalized.ChainID = cfg.ChainID
	}
	if genesis := strings.TrimSpace(cfg.GenesisAddress); genesis != "" {
		normalized.GenesisAddress = genesis
	}
	if architect := strings.TrimSpace(cfg.ArchitectAddress); architect != "" {
		normalized.ArchitectAddress = architect
	}
	if supply := strings.TrimSpace(cfg.CirculatingSupply); supply != "" {
		normalized.CirculatingSupply = supply
	}
	normalized.Bootnodes = normalizeBootnodes(cfg.Bootnodes)
	if len(cfg.SystemContracts) > 0 {
		normalized.SystemContracts = cloneContractRecords(cfg.SystemContracts)
	}

	if normalized.ChainID == 0 {
		return NetworkConfig{}, fmt.Errorf("core: chain ID is required")
	}
	if err := validateProtocolAddress("genesis address", normalized.GenesisAddress); err != nil {
		return NetworkConfig{}, err
	}
	if err := validateProtocolAddress("architect address", normalized.ArchitectAddress); err != nil {
		return NetworkConfig{}, err
	}

	return normalized, nil
}

func LoadNetworkConfig(path string) (NetworkConfig, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return NetworkConfig{}, err
	}

	var cfg NetworkConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return NetworkConfig{}, err
	}
	return NormalizeNetworkConfig(cfg)
}

func WriteNetworkConfig(path string, cfg NetworkConfig) error {
	normalized, err := NormalizeNetworkConfig(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	payload, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func cloneContractRecords(records []ContractRecord) []ContractRecord {
	cloned := make([]ContractRecord, 0, len(records))
	for _, record := range records {
		copyRecord := record
		copyRecord.Functions = append([]ContractFunction(nil), record.Functions...)
		cloned = append(cloned, copyRecord)
	}
	return cloned
}

func normalizeBootnodes(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		cleaned := strings.TrimSpace(value)
		if cleaned == "" {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func validateProtocolAddress(label, value string) error {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" || strings.Contains(cleaned, "REPLACE_ME") {
		return nil
	}
	if _, err := types.ParseAddress(cleaned); err != nil {
		return fmt.Errorf("core: invalid %s: %w", label, err)
	}
	return nil
}
