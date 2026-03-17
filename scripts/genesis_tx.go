package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"unified/core"
	"unified/core/consensus"
	"unified/core/constants"
	"unified/core/types"
)

const (
	ufdScale               = int64(constants.BasisPoints)
	defaultRPCURL          = "http://localhost:8545"
	defaultGenesisURL      = "https://unified.network/genesis"
	defaultGenesisQuery    = "UniFied genesis seed"
	defaultRegistrationFee = "10.0"
	defaultSeedBaseBounty  = "50.0"
	defaultSeedDifficulty  = uint64(64)
	defaultSeedDataVolume  = uint64(4096)
	waitBlockTimeout       = 2 * time.Minute
	pollInterval           = 2 * time.Second
)

type rpcClient struct {
	url    string
	client *http.Client
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type blockEnvelope struct {
	Hash   string          `json:"hash"`
	Header blockHeaderJSON `json:"header"`
	Body   blockBodyJSON   `json:"body"`
}

type blockHeaderJSON struct {
	Number uint64 `json:"number"`
}

type blockBodyJSON struct {
	Transactions []core.Transaction `json:"transactions"`
}

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags)

	privateKey, err := parseArchitectKey(os.Getenv("UFI_ARCHITECT_KEY"))
	if err != nil {
		logger.Fatal(err)
	}

	architectAddress, err := types.NewAddressFromPubKey(privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		logger.Fatal(err)
	}
	if err := validateExpectedArchitectAddress(architectAddress.String()); err != nil {
		logger.Fatal(err)
	}

	client := &rpcClient{
		url:    strings.TrimSpace(envOrDefault("UFI_RPC_URL", defaultRPCURL)),
		client: &http.Client{Timeout: 15 * time.Second},
	}

	latest, err := client.latestBlock()
	if err != nil {
		logger.Fatal(err)
	}
	if latest.Header.Number > 0 {
		logger.Fatalf("refusing genesis flow: latest block is already #%d", latest.Header.Number)
	}

	registrationFee, err := client.namePrice("Architect")
	if err != nil {
		registrationFee, err = parseUFDAmount(defaultRegistrationFee)
		if err != nil {
			logger.Fatal(err)
		}
		logger.Printf("native UNS price quote unavailable, falling back to default registration fee %s UFD", formatUFDAmount(registrationFee))
	} else {
		logger.Printf("quoted Architect UNS registration fee %s UFD from node", formatUFDAmount(registrationFee))
	}
	seedBaseBounty, err := parseUFDAmount(defaultSeedBaseBounty)
	if err != nil {
		logger.Fatal(err)
	}
	seedBounty, err := consensus.QuoteBounty(seedBaseBounty, defaultSeedDifficulty, defaultSeedDataVolume)
	if err != nil {
		logger.Fatal(err)
	}
	seedArchitectFee := constants.ArchitectFee(seedBounty)
	seedMinerReward := new(big.Int).Sub(new(big.Int).Set(seedBounty), seedArchitectFee)
	if new(big.Int).Add(new(big.Int).Set(seedArchitectFee), seedMinerReward).Cmp(seedBounty) != 0 {
		logger.Fatal("seed task fee split is invalid")
	}

	currentBalance, err := client.balanceOf(architectAddress.String())
	if err != nil {
		logger.Fatal(err)
	}
	totalRequired := new(big.Int).Add(new(big.Int).Set(registrationFee), seedBounty)
	if currentBalance.Cmp(totalRequired) < 0 {
		logger.Fatalf(
			"insufficient architect balance: have %s UFD, need %s UFD. start the node with --operator %s or seed this address first",
			formatUFDAmount(currentBalance),
			formatUFDAmount(totalRequired),
			architectAddress.String(),
		)
	}

	registrationData, err := core.EncodeRegisterNameCall("Architect")
	if err != nil {
		logger.Fatal(err)
	}
	registrationTx := core.Transaction{
		Type:  core.TxTypeTransfer,
		From:  architectAddress.String(),
		To:    constants.UNSRegistryAddress,
		Value: registrationFee.String(),
		Nonce: 0,
		Data:  registrationData,
	}
	if err := registrationTx.Sign(privateKey); err != nil {
		logger.Fatal(err)
	}

	registrationHash, err := client.sendRawTransaction(registrationTx)
	if err != nil {
		logger.Fatal(err)
	}
	logger.Printf("broadcasted Architect UNS registration tx %s to %s", registrationHash, constants.UNSRegistryAddress)

	blockOne, err := client.waitForBlock(1, waitBlockTimeout)
	if err != nil {
		logger.Fatal(err)
	}
	if !blockContainsTransaction(blockOne, registrationHash) {
		logger.Fatalf("block #1 mined without the genesis registration tx %s", registrationHash)
	}
	logger.Printf("block #1 mined with genesis tx %s", registrationHash)

	seedRequest := core.SearchTaskRequest{
		Query:           envOrDefault("UFI_GENESIS_QUERY", defaultGenesisQuery),
		URL:             envOrDefault("UFI_GENESIS_URL", defaultGenesisURL),
		BaseBounty:      seedBaseBounty.String(),
		Difficulty:      defaultSeedDifficulty,
		DataVolumeBytes: defaultSeedDataVolume,
	}
	seedPayload, err := json.Marshal(seedRequest)
	if err != nil {
		logger.Fatal(err)
	}
	seedTx := core.Transaction{
		Type:  core.TxTypeSearchTask,
		From:  architectAddress.String(),
		Value: seedBounty.String(),
		Nonce: 1,
		Data:  seedPayload,
	}
	if err := seedTx.Sign(privateKey); err != nil {
		logger.Fatal(err)
	}

	seedHash, err := client.sendRawTransaction(seedTx)
	if err != nil {
		logger.Fatal(err)
	}
	logger.Printf(
		"seed task submitted: tx=%s url=%s total=%s UFD architect_fee=%s UFD miner_reward=%s UFD",
		seedHash,
		seedRequest.URL,
		formatUFDAmount(seedBounty),
		formatUFDAmount(seedArchitectFee),
		formatUFDAmount(seedMinerReward),
	)

	logger.Printf("UniFied Genesis Transaction successful. Block #1 initialized. Architect identity secured.")
}

func (c *rpcClient) call(method string, params any, result any) error {
	requestBody, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}

	response, err := c.client.Post(c.url, "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}

	var rpc rpcResponse
	if err := json.Unmarshal(body, &rpc); err != nil {
		return err
	}
	if rpc.Error != nil {
		return fmt.Errorf("rpc %s failed (%d): %s", method, rpc.Error.Code, rpc.Error.Message)
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(rpc.Result, result)
}

func (c *rpcClient) sendRawTransaction(tx core.Transaction) (string, error) {
	payload, err := json.Marshal(tx)
	if err != nil {
		return "", err
	}

	var result struct {
		Hash string `json:"hash"`
	}
	if err := c.call("ufi_sendRawTransaction", map[string]string{
		"raw": "0x" + hex.EncodeToString(payload),
	}, &result); err != nil {
		return "", err
	}
	return result.Hash, nil
}

func (c *rpcClient) latestBlock() (blockEnvelope, error) {
	var block blockEnvelope
	err := c.call("ufi_getBlockByNumber", map[string]string{"number": "latest"}, &block)
	return block, err
}

func (c *rpcClient) waitForBlock(number uint64, timeout time.Duration) (blockEnvelope, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		latest, err := c.latestBlock()
		if err != nil {
			return blockEnvelope{}, err
		}
		if latest.Header.Number >= number {
			var block blockEnvelope
			if err := c.call("ufi_getBlockByNumber", map[string]string{
				"number": fmt.Sprintf("%d", number),
			}, &block); err != nil {
				return blockEnvelope{}, err
			}
			return block, nil
		}
		time.Sleep(pollInterval)
	}
	return blockEnvelope{}, fmt.Errorf("timed out waiting for block #%d", number)
}

func (c *rpcClient) balanceOf(address string) (*big.Int, error) {
	var result struct {
		Balance string `json:"balance"`
	}
	if err := c.call("ufi_getBalance", map[string]string{"address": address}, &result); err != nil {
		return nil, err
	}
	value, ok := new(big.Int).SetString(strings.TrimSpace(result.Balance), 10)
	if !ok {
		return nil, fmt.Errorf("invalid balance returned by node")
	}
	return value, nil
}

func (c *rpcClient) namePrice(name string) (*big.Int, error) {
	var result struct {
		Price string `json:"price"`
	}
	if err := c.call("ufi_getNamePrice", map[string]string{"name": name}, &result); err != nil {
		return nil, err
	}
	value, ok := new(big.Int).SetString(strings.TrimSpace(result.Price), 10)
	if !ok {
		return nil, fmt.Errorf("invalid name price returned by node")
	}
	return value, nil
}

func blockContainsTransaction(block blockEnvelope, hash string) bool {
	for _, tx := range block.Body.Transactions {
		if tx.Hash == hash {
			return true
		}
	}
	return false
}

func parseArchitectKey(raw string) (ed25519.PrivateKey, error) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return nil, fmt.Errorf("UFI_ARCHITECT_KEY is required")
	}

	var bytes []byte
	if decoded, err := hex.DecodeString(strings.TrimPrefix(cleaned, "0x")); err == nil {
		bytes = decoded
	} else if decoded, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
		bytes = decoded
	} else {
		return nil, fmt.Errorf("UFI_ARCHITECT_KEY must be hex or base64 encoded")
	}

	switch len(bytes) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(bytes), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(bytes), nil
	default:
		return nil, fmt.Errorf("UFI_ARCHITECT_KEY must decode to %d-byte seed or %d-byte private key", ed25519.SeedSize, ed25519.PrivateKeySize)
	}
}

func validateExpectedArchitectAddress(derived string) error {
	expected := strings.TrimSpace(os.Getenv("UFI_ARCHITECT_ADDRESS"))
	if expected == "" && !strings.Contains(constants.GenesisArchitectAddress, "REPLACE_ME") {
		expected = constants.GenesisArchitectAddress
	}
	if expected == "" {
		return nil
	}
	if expected != derived {
		return fmt.Errorf("architect key derives %s, expected %s", derived, expected)
	}
	return nil
}

func parseUFDAmount(value string) (*big.Int, error) {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return nil, fmt.Errorf("empty UFD amount")
	}
	if strings.HasPrefix(cleaned, "-") {
		return nil, fmt.Errorf("negative UFD amount: %s", value)
	}

	parts := strings.SplitN(cleaned, ".", 2)
	whole, ok := new(big.Int).SetString(parts[0], 10)
	if !ok {
		return nil, fmt.Errorf("invalid UFD amount: %s", value)
	}

	result := new(big.Int).Mul(whole, big.NewInt(ufdScale))
	if len(parts) == 1 {
		return result, nil
	}

	fraction := parts[1]
	if len(fraction) > 4 {
		return nil, fmt.Errorf("UFD amount supports at most 4 decimal places: %s", value)
	}
	fraction += strings.Repeat("0", 4-len(fraction))
	fractionValue, ok := new(big.Int).SetString(fraction, 10)
	if !ok {
		return nil, fmt.Errorf("invalid UFD amount: %s", value)
	}
	return result.Add(result, fractionValue), nil
}

func formatUFDAmount(value *big.Int) string {
	if value == nil {
		return "0.0000"
	}

	sign := ""
	amount := new(big.Int).Set(value)
	if amount.Sign() < 0 {
		sign = "-"
		amount.Neg(amount)
	}

	quotient := new(big.Int).Quo(amount, big.NewInt(ufdScale))
	remainder := new(big.Int).Mod(amount, big.NewInt(ufdScale))
	return fmt.Sprintf("%s%s.%04s", sign, quotient.String(), leftPad(remainder.String(), 4))
}

func leftPad(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return strings.Repeat("0", width-len(value)) + value
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
