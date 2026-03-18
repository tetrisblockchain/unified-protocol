package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"unified/core"
	"unified/core/constants"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		output            string
		name              string
		genesisAddress    string
		architectAddress  string
		circulatingSupply string
		bootnodesRaw      string
		chainID           uint64
	)

	flag.StringVar(&output, "output", "", "path to write the network config JSON; omit to print to stdout")
	flag.StringVar(&name, "name", "unified-mainnet", "network name")
	flag.Uint64Var(&chainID, "chain-id", constants.DefaultChainID, "chain ID")
	flag.StringVar(&genesisAddress, "genesis-address", "", "shared genesis-funded address")
	flag.StringVar(&architectAddress, "architect-address", "", "architect treasury address")
	flag.StringVar(&circulatingSupply, "circulating-supply", "1000000", "circulating supply")
	flag.StringVar(&bootnodesRaw, "bootnodes", "", "comma-separated bootnode multiaddrs")
	flag.Parse()

	config, err := core.NormalizeNetworkConfig(core.NetworkConfig{
		Name:              name,
		ChainID:           chainID,
		GenesisAddress:    genesisAddress,
		ArchitectAddress:  architectAddress,
		CirculatingSupply: circulatingSupply,
		Bootnodes:         splitCSV(bootnodesRaw),
	})
	if err != nil {
		return err
	}

	if strings.TrimSpace(output) == "" {
		payload, err := marshalConfig(config)
		if err != nil {
			return err
		}
		fmt.Print(payload)
		return nil
	}

	if err := core.WriteNetworkConfig(output, config); err != nil {
		return err
	}
	fmt.Printf("wrote network config to %s\n", output)
	return nil
}

func marshalConfig(config core.NetworkConfig) (string, error) {
	payload, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	return string(append(payload, '\n')), nil
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if cleaned := strings.TrimSpace(part); cleaned != "" {
			out = append(out, cleaned)
		}
	}
	return out
}
