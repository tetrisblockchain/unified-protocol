package core

import (
	"context"
	"crypto/ed25519"
	"math/big"
	"path/filepath"
	"testing"

	"unified/core/types"
)

func TestImportRemoteBlockBuffersFutureChild(t *testing.T) {
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

	chain, err := OpenBlockchain(BlockchainConfig{
		DataDir: filepath.Join(t.TempDir(), "chain"),
		GenesisBalances: map[string]*big.Int{
			sender.String(): big.NewInt(100_000),
		},
	})
	if err != nil {
		t.Fatalf("OpenBlockchain returned error: %v", err)
	}
	defer chain.Close()

	txOne := Transaction{
		Type:  TxTypeTransfer,
		From:  sender.String(),
		To:    recipient.String(),
		Value: "1000",
		Nonce: 0,
	}
	if err := txOne.Sign(privateKey); err != nil {
		t.Fatalf("sign txOne: %v", err)
	}
	blockOne, err := chain.MineBlock(sender.String(), []Transaction{txOne}, nil)
	if err != nil {
		t.Fatalf("MineBlock blockOne returned error: %v", err)
	}

	remote, err := OpenBlockchain(BlockchainConfig{
		DataDir: filepath.Join(t.TempDir(), "remote"),
		GenesisBalances: map[string]*big.Int{
			sender.String(): big.NewInt(100_000),
		},
	})
	if err != nil {
		t.Fatalf("OpenBlockchain remote returned error: %v", err)
	}
	defer remote.Close()

	txTwo := Transaction{
		Type:  TxTypeTransfer,
		From:  sender.String(),
		To:    recipient.String(),
		Value: "2000",
		Nonce: 1,
	}
	if err := txTwo.Sign(privateKey); err != nil {
		t.Fatalf("sign txTwo: %v", err)
	}
	blockTwo, err := chain.MineBlock(sender.String(), []Transaction{txTwo}, nil)
	if err != nil {
		t.Fatalf("MineBlock blockTwo returned error: %v", err)
	}

	forkChoice := NewForkChoice(remote.LatestBlock())
	if err := ImportRemoteBlock(context.Background(), remote, nil, forkChoice, blockTwo); err != nil {
		t.Fatalf("ImportRemoteBlock blockTwo returned error: %v", err)
	}
	if remote.LatestBlock().Header.Number != 0 {
		t.Fatalf("latest after future block = %d, want 0", remote.LatestBlock().Header.Number)
	}

	if err := ImportRemoteBlock(context.Background(), remote, nil, forkChoice, blockOne); err != nil {
		t.Fatalf("ImportRemoteBlock blockOne returned error: %v", err)
	}
	if remote.LatestBlock().Header.Number != 2 {
		t.Fatalf("latest after buffered import = %d, want 2", remote.LatestBlock().Header.Number)
	}
}
