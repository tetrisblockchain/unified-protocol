package core

import (
	"math/big"
	"path/filepath"
	"testing"
)

func TestReadinessReportFlagsLocalDefaults(t *testing.T) {
	t.Parallel()

	chain, err := OpenBlockchain(BlockchainConfig{
		DataDir:         filepath.Join(t.TempDir(), "chain"),
		GenesisBalances: map[string]*big.Int{},
	})
	if err != nil {
		t.Fatalf("OpenBlockchain returned error: %v", err)
	}
	defer chain.Close()

	report := chain.ReadinessReport(ReadinessContext{})
	if report.Ready {
		t.Fatalf("report.Ready = true, want false for local/temp config")
	}
	if len(report.Checks) == 0 {
		t.Fatalf("report.Checks = 0, want checks")
	}
}
