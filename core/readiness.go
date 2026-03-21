package core

import (
	"os"
	"path/filepath"
	"strings"

	"unified/core/types"
)

type ReadinessContext struct {
	ConnectedPeers     int  `json:"connectedPeers"`
	LowReputationPeers int  `json:"lowReputationPeers"`
	BannedPeers        int  `json:"bannedPeers"`
	MiningEnabled      bool `json:"miningEnabled"`
}

type ReadinessCheck struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail"`
}

type ReadinessReport struct {
	Ready          bool             `json:"ready"`
	Network        NetworkConfig    `json:"network"`
	LatestBlock    uint64           `json:"latestBlock"`
	DataDir        string           `json:"dataDir"`
	ConnectedPeers int              `json:"connectedPeers"`
	Checks         []ReadinessCheck `json:"checks"`
}

func (bc *Blockchain) ReadinessReport(ctx ReadinessContext) ReadinessReport {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	report := ReadinessReport{
		Network:        bc.network.Clone(),
		LatestBlock:    bc.latest.Header.Number,
		DataDir:        bc.datadir,
		ConnectedPeers: ctx.ConnectedPeers,
	}

	checks := []ReadinessCheck{
		{
			Name:     "network-name",
			Severity: "required",
			OK:       strings.TrimSpace(report.Network.Name) != "" && report.Network.Name != "unified-local",
			Detail:   "shared network should use an explicit non-local network name",
		},
		{
			Name:     "chain-id",
			Severity: "required",
			OK:       report.Network.ChainID != 0,
			Detail:   "chain ID must be configured and persisted",
		},
		{
			Name:     "genesis-address",
			Severity: "required",
			OK:       readinessAddressOK(report.Network.GenesisAddress),
			Detail:   "genesis address must be a valid non-placeholder UFI address",
		},
		{
			Name:     "architect-address",
			Severity: "required",
			OK:       readinessAddressOK(report.Network.ArchitectAddress),
			Detail:   "architect address must be a valid non-placeholder UFI address",
		},
		{
			Name:     "persistent-datadir",
			Severity: "required",
			OK:       !isEphemeralDataDir(report.DataDir),
			Detail:   "node should not run from a temporary or local-dev datadir",
		},
		{
			Name:     "protocol-contracts",
			Severity: "required",
			OK:       readinessContractsOK(report.Network.SystemContracts),
			Detail:   "protocol contracts must be published in the shared network config",
		},
		{
			Name:     "peer-connectivity",
			Severity: "warning",
			OK:       len(report.Network.Bootnodes) == 0 || ctx.ConnectedPeers > 0,
			Detail:   "node should have live peer connectivity when bootnodes are configured",
		},
		{
			Name:     "peer-reputation",
			Severity: "warning",
			OK:       ctx.BannedPeers == 0 && ctx.LowReputationPeers == 0,
			Detail:   "no connected peers should currently be degraded or banned",
		},
		{
			Name:     "mining-enabled",
			Severity: "warning",
			OK:       ctx.MiningEnabled,
			Detail:   "at least one production seed node should have mining enabled",
		},
	}
	report.Checks = checks
	report.Ready = true
	for _, check := range checks {
		if check.Severity == "required" && !check.OK {
			report.Ready = false
			break
		}
	}
	return report
}

func readinessAddressOK(address string) bool {
	cleaned := strings.TrimSpace(address)
	if cleaned == "" || strings.Contains(cleaned, "REPLACE_ME") {
		return false
	}
	_, err := types.ParseAddress(cleaned)
	return err == nil
}

func readinessContractsOK(contracts []ContractRecord) bool {
	if len(contracts) < 2 {
		return false
	}
	foundSearch := false
	foundUNS := false
	for _, contract := range contracts {
		if !contract.GenesisDeployed {
			return false
		}
		switch contract.Address {
		case "0x101":
			foundSearch = true
		case "0x102":
			foundUNS = true
		}
	}
	return foundSearch && foundUNS
}

func isEphemeralDataDir(path string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "." || cleaned == "" {
		return true
	}
	tempRoot := filepath.Clean(os.TempDir())
	if cleaned == tempRoot || strings.HasPrefix(cleaned, tempRoot+string(os.PathSeparator)) {
		return true
	}
	cleaned = filepath.ToSlash(cleaned)
	return strings.HasSuffix(cleaned, "/data/local")
}
