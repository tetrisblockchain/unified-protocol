package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"unified/core/types"
)

type identityOutput struct {
	Address      string `json:"address"`
	SeedHex      string `json:"seedHex"`
	SeedBase64   string `json:"seedBase64"`
	PublicKeyHex string `json:"publicKeyHex"`
}

func main() {
	var (
		rawSeed string
		asJSON  bool
		asEnv   bool
		alias   string
	)

	flag.StringVar(&rawSeed, "seed", "", "existing operator seed/private key in hex or base64; omit to generate a new one")
	flag.BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	flag.BoolVar(&asEnv, "env", false, "print shell export lines")
	flag.StringVar(&alias, "alias", "", "optional alias to include in env output")
	flag.Parse()

	privateKey, generated, err := loadOrGeneratePrivateKey(rawSeed)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	seed := privateKey.Seed()
	publicKey := privateKey.Public().(ed25519.PublicKey)
	address, err := types.NewAddressFromPubKey(publicKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	output := identityOutput{
		Address:      address.String(),
		SeedHex:      hex.EncodeToString(seed),
		SeedBase64:   base64.StdEncoding.EncodeToString(seed),
		PublicKeyHex: hex.EncodeToString(publicKey),
	}

	switch {
	case asJSON:
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(output)
	case asEnv:
		fmt.Printf("export UFI_OPERATOR_KEY=%q\n", output.SeedHex)
		fmt.Printf("export UNIFIED_OPERATOR_ADDRESS=%q\n", output.Address)
		if strings.TrimSpace(alias) != "" {
			fmt.Printf("export UNIFIED_OPERATOR_ALIAS=%q\n", strings.TrimSpace(alias))
		}
	default:
		if generated {
			fmt.Println("Generated a new operator identity.")
		} else {
			fmt.Println("Derived operator identity from the supplied seed.")
		}
		fmt.Printf("UNIFIED_OPERATOR_ADDRESS=%s\n", output.Address)
		fmt.Printf("UFI_OPERATOR_KEY=%s\n", output.SeedHex)
		fmt.Printf("PublicKey=%s\n", output.PublicKeyHex)
		fmt.Println("Store UFI_OPERATOR_KEY securely. The node config only needs UNIFIED_OPERATOR_ADDRESS, but the key is required for future signing flows.")
	}
}

func loadOrGeneratePrivateKey(raw string) (ed25519.PrivateKey, bool, error) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		seed := make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return nil, false, fmt.Errorf("generate operator seed: %w", err)
		}
		return ed25519.NewKeyFromSeed(seed), true, nil
	}

	decoded, err := decodeFlexibleBytes(cleaned)
	if err != nil {
		return nil, false, fmt.Errorf("parse operator seed: %w", err)
	}

	switch len(decoded) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(decoded), false, nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(decoded), false, nil
	default:
		return nil, false, fmt.Errorf("operator seed must decode to %d-byte seed or %d-byte private key", ed25519.SeedSize, ed25519.PrivateKeySize)
	}
}

func decodeFlexibleBytes(raw string) ([]byte, error) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return nil, fmt.Errorf("empty seed")
	}

	if decoded, err := hex.DecodeString(strings.TrimPrefix(cleaned, "0x")); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("value must be hex or base64 encoded")
}
