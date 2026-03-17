package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"unified/core"
	"unified/core/constants"
	"unified/core/types"
)

const (
	defaultSeedRPCURL        = "http://localhost:8545"
	defaultSeedQuery         = "initial web seed"
	defaultSeedBaseBountyUFD = "1.0"
	defaultSeedDifficulty    = uint64(8)
	defaultSeedDataVolume    = uint64(1024)
	defaultSeedBatchSize     = 32
	defaultSeedPollInterval  = 3 * time.Second
	defaultSeedHTTPTimeout   = 20 * time.Second
	maxSeedSenderPending     = 32
	seedUFDScale             = int64(constants.BasisPoints)
)

type seedRPCClient struct {
	rpcURL  string
	baseURL string
	client  *http.Client
}

type seedRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type seedRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *seedRPCError   `json:"error"`
}

type seedRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type seedQuoteResponse struct {
	Query              string   `json:"query"`
	URL                string   `json:"url"`
	MultiplierBPS      uint64   `json:"multiplierBps"`
	AdjustedDifficulty uint64   `json:"adjustedDifficulty"`
	AdjustedBounty     string   `json:"adjustedBounty"`
	ArchitectFee       string   `json:"architectFee"`
	MinerReward        string   `json:"minerReward"`
	PrioritySectors    []string `json:"prioritySectors"`
}

type seedFileEntry struct {
	URL   string
	Query string
}

type seedPlanItem struct {
	Entry seedFileEntry
	Quote seedQuoteResponse
}

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags)

	var (
		filePath        string
		rpcURL          string
		defaultQuery    string
		baseBountyUFD   string
		difficulty      uint64
		dataVolumeBytes uint64
		batchSize       int
		pollIntervalRaw time.Duration
	)

	flag.StringVar(&filePath, "file", strings.TrimSpace(os.Getenv("UFI_URLS_FILE")), "path to a newline-delimited URL file; each line may also be url,query")
	flag.StringVar(&rpcURL, "rpc-url", envOrDefault("UFI_RPC_URL", defaultSeedRPCURL), "node RPC endpoint, with or without /rpc")
	flag.StringVar(&defaultQuery, "query", envOrDefault("UFI_SEED_QUERY", defaultSeedQuery), "default search query to associate with each URL")
	flag.StringVar(&baseBountyUFD, "base-bounty", envOrDefault("UFI_SEED_BASE_BOUNTY", defaultSeedBaseBountyUFD), "base bounty in UFD, supports up to 4 decimal places")
	flag.Uint64Var(&difficulty, "difficulty", envOrDefaultUint64("UFI_SEED_DIFFICULTY", defaultSeedDifficulty), "task difficulty before governance adjustment")
	flag.Uint64Var(&dataVolumeBytes, "data-volume-bytes", envOrDefaultUint64("UFI_SEED_DATA_VOLUME_BYTES", defaultSeedDataVolume), "data volume input for bounty quoting")
	flag.IntVar(&batchSize, "batch-size", envOrDefaultInt("UFI_SEED_BATCH_SIZE", defaultSeedBatchSize), "max in-flight tasks from this sender")
	flag.DurationVar(&pollIntervalRaw, "poll-interval", defaultSeedPollInterval, "poll interval while waiting for pending tasks to drain")
	flag.Parse()

	if strings.TrimSpace(filePath) == "" {
		logger.Fatal("--file is required")
	}
	defaultQuery = strings.TrimSpace(defaultQuery)
	if defaultQuery == "" {
		logger.Fatal("--query must not be empty")
	}
	if batchSize <= 0 || batchSize > maxSeedSenderPending {
		logger.Fatalf("--batch-size must be between 1 and %d", maxSeedSenderPending)
	}

	privateKey, err := parseSeedKey(os.Getenv("UFI_ARCHITECT_KEY"))
	if err != nil {
		logger.Fatal(err)
	}
	address, err := types.NewAddressFromPubKey(privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		logger.Fatal(err)
	}

	client, err := newSeedRPCClient(rpcURL)
	if err != nil {
		logger.Fatal(err)
	}

	entries, err := loadSeedFile(filePath, defaultQuery)
	if err != nil {
		logger.Fatal(err)
	}
	if len(entries) == 0 {
		logger.Fatalf("no URLs found in %s", filePath)
	}

	baseBounty, err := parseSeedUFDAmount(baseBountyUFD)
	if err != nil {
		logger.Fatal(err)
	}

	latestNonce, err := client.transactionCount(address.String(), "latest")
	if err != nil {
		logger.Fatal(err)
	}
	startingLatestNonce := latestNonce
	pendingNonce, err := client.transactionCount(address.String(), "pending")
	if err != nil {
		logger.Fatal(err)
	}
	if pendingNonce != latestNonce {
		logger.Fatalf("refusing to seed while sender already has pending transactions: latest=%d pending=%d", latestNonce, pendingNonce)
	}

	plan := make([]seedPlanItem, 0, len(entries))
	totalRequired := big.NewInt(0)
	totalArchitectFee := big.NewInt(0)
	totalMinerReward := big.NewInt(0)
	for _, entry := range entries {
		request := core.SearchTaskRequest{
			Query:           entry.Query,
			URL:             entry.URL,
			BaseBounty:      baseBounty.String(),
			Difficulty:      difficulty,
			DataVolumeBytes: dataVolumeBytes,
		}
		quote, err := client.quoteTask(request)
		if err != nil {
			logger.Fatalf("quote %s failed: %v", entry.URL, err)
		}
		adjustedBounty, ok := new(big.Int).SetString(strings.TrimSpace(quote.AdjustedBounty), 10)
		if !ok {
			logger.Fatalf("quote %s returned invalid adjusted bounty %q", entry.URL, quote.AdjustedBounty)
		}
		architectFee, ok := new(big.Int).SetString(strings.TrimSpace(quote.ArchitectFee), 10)
		if !ok {
			logger.Fatalf("quote %s returned invalid architect fee %q", entry.URL, quote.ArchitectFee)
		}
		minerReward, ok := new(big.Int).SetString(strings.TrimSpace(quote.MinerReward), 10)
		if !ok {
			logger.Fatalf("quote %s returned invalid miner reward %q", entry.URL, quote.MinerReward)
		}
		plan = append(plan, seedPlanItem{Entry: entry, Quote: quote})
		totalRequired.Add(totalRequired, adjustedBounty)
		totalArchitectFee.Add(totalArchitectFee, architectFee)
		totalMinerReward.Add(totalMinerReward, minerReward)
	}

	balance, err := client.balanceOf(address.String())
	if err != nil {
		logger.Fatal(err)
	}
	if balance.Cmp(totalRequired) < 0 {
		logger.Fatalf(
			"insufficient balance: have %s UFD, need %s UFD to seed %d URLs",
			formatSeedUFDAmount(balance),
			formatSeedUFDAmount(totalRequired),
			len(plan),
		)
	}

	logger.Printf(
		"preflight ok: sender=%s urls=%d total=%s UFD architect_fee=%s UFD miner_reward=%s UFD",
		address.String(),
		len(plan),
		formatSeedUFDAmount(totalRequired),
		formatSeedUFDAmount(totalArchitectFee),
		formatSeedUFDAmount(totalMinerReward),
	)

	nextNonce := pendingNonce
	nextIndex := 0
	for nextIndex < len(plan) {
		latestNonce, err = client.transactionCount(address.String(), "latest")
		if err != nil {
			logger.Fatal(err)
		}
		pendingNonce, err = client.transactionCount(address.String(), "pending")
		if err != nil {
			logger.Fatal(err)
		}
		if pendingNonce < latestNonce {
			logger.Fatalf("node reported pending nonce %d below latest nonce %d", pendingNonce, latestNonce)
		}

		inFlight := int(pendingNonce - latestNonce)
		if inFlight >= batchSize {
			logger.Printf("waiting for mining: submitted=%d/%d latest=%d pending=%d", nextIndex, len(plan), latestNonce, pendingNonce)
			time.Sleep(pollIntervalRaw)
			continue
		}

		slots := batchSize - inFlight
		submitted := 0
		for submitted < slots && nextIndex < len(plan) {
			item := plan[nextIndex]
			request := core.SearchTaskRequest{
				Query:           item.Entry.Query,
				URL:             item.Entry.URL,
				BaseBounty:      baseBounty.String(),
				Difficulty:      difficulty,
				DataVolumeBytes: dataVolumeBytes,
			}
			payload, err := json.Marshal(request)
			if err != nil {
				logger.Fatal(err)
			}

			tx := core.Transaction{
				Type:  core.TxTypeSearchTask,
				From:  address.String(),
				Value: item.Quote.AdjustedBounty,
				Nonce: nextNonce,
				Data:  payload,
			}
			if err := tx.Sign(privateKey); err != nil {
				logger.Fatal(err)
			}

			hash, err := client.sendRawTransaction(tx)
			if err != nil {
				if isTransientSeedError(err) {
					logger.Printf("submit delayed for %s: %v", item.Entry.URL, err)
					time.Sleep(pollIntervalRaw)
					break
				}
				logger.Fatalf("submit %s failed: %v", item.Entry.URL, err)
			}

			logger.Printf(
				"submitted %d/%d nonce=%d tx=%s url=%s total=%s UFD",
				nextIndex+1,
				len(plan),
				nextNonce,
				hash,
				item.Entry.URL,
				formatSeedUFDAmount(mustParseBigInt(item.Quote.AdjustedBounty)),
			)
			nextNonce++
			nextIndex++
			submitted++
		}
	}

	targetLatestNonce := startingLatestNonce + uint64(len(plan))
	for {
		latestNonce, err = client.transactionCount(address.String(), "latest")
		if err != nil {
			logger.Fatal(err)
		}
		pendingNonce, err = client.transactionCount(address.String(), "pending")
		if err != nil {
			logger.Fatal(err)
		}
		if latestNonce >= targetLatestNonce {
			break
		}
		logger.Printf("waiting for final indexing: latest=%d pending=%d target=%d", latestNonce, pendingNonce, targetLatestNonce)
		time.Sleep(pollIntervalRaw)
	}

	logger.Printf(
		"seed complete: indexed %d URLs from %s through nonce %d",
		len(plan),
		filepath.Base(filePath),
		targetLatestNonce-1,
	)
}

func newSeedRPCClient(raw string) (*seedRPCClient, error) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		cleaned = defaultSeedRPCURL
	}

	parsed, err := neturl.Parse(cleaned)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" {
		return nil, fmt.Errorf("rpc url must include scheme, got %q", raw)
	}

	base := strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/")
	rpcPath := strings.TrimRight(parsed.Path, "/")
	rpcURL := cleaned
	switch rpcPath {
	case "", "/":
		rpcURL = base
	case "/rpc":
		rpcURL = base + "/rpc"
	default:
		rpcURL = cleaned
		base = strings.TrimRight(cleaned, "/")
	}

	return &seedRPCClient{
		rpcURL:  rpcURL,
		baseURL: base,
		client:  &http.Client{Timeout: defaultSeedHTTPTimeout},
	}, nil
}

func (c *seedRPCClient) call(method string, params any, result any) error {
	body, err := json.Marshal(seedRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}

	response, err := c.client.Post(c.rpcURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}

	var rpc seedRPCResponse
	if err := json.Unmarshal(payload, &rpc); err != nil {
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

func (c *seedRPCClient) balanceOf(address string) (*big.Int, error) {
	var result struct {
		Balance string `json:"balance"`
	}
	if err := c.call("ufi_getBalance", map[string]string{"address": address}, &result); err != nil {
		return nil, err
	}
	value, ok := new(big.Int).SetString(strings.TrimSpace(result.Balance), 10)
	if !ok {
		return nil, fmt.Errorf("invalid balance %q", result.Balance)
	}
	return value, nil
}

func (c *seedRPCClient) transactionCount(address, block string) (uint64, error) {
	var result struct {
		Nonce string `json:"nonce"`
	}
	if err := c.call("ufi_getTransactionCount", map[string]string{
		"address": address,
		"block":   block,
	}, &result); err != nil {
		return 0, err
	}
	return parseSeedUint(result.Nonce)
}

func (c *seedRPCClient) sendRawTransaction(tx core.Transaction) (string, error) {
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

func (c *seedRPCClient) quoteTask(request core.SearchTaskRequest) (seedQuoteResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return seedQuoteResponse{}, err
	}
	response, err := c.client.Post(c.baseURL+"/consensus/quote", "application/json", bytes.NewReader(body))
	if err != nil {
		return seedQuoteResponse{}, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(response.Body)
		return seedQuoteResponse{}, fmt.Errorf("quote failed: %s", strings.TrimSpace(string(payload)))
	}

	var quote seedQuoteResponse
	if err := json.NewDecoder(response.Body).Decode(&quote); err != nil {
		return seedQuoteResponse{}, err
	}
	return quote, nil
}

func loadSeedFile(path string, defaultQuery string) ([]seedFileEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	entries := make([]seedFileEntry, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}

		entry, err := parseSeedLine(raw, defaultQuery)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNumber, err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func parseSeedLine(raw, defaultQuery string) (seedFileEntry, error) {
	if strings.Contains(raw, ",") {
		reader := csv.NewReader(strings.NewReader(raw))
		fields, err := reader.Read()
		if err != nil {
			return seedFileEntry{}, err
		}
		if len(fields) == 0 {
			return seedFileEntry{}, fmt.Errorf("missing url")
		}
		url := strings.TrimSpace(fields[0])
		query := strings.TrimSpace(defaultQuery)
		if len(fields) > 1 && strings.TrimSpace(fields[1]) != "" {
			query = strings.TrimSpace(fields[1])
		}
		if url == "" {
			return seedFileEntry{}, fmt.Errorf("missing url")
		}
		return seedFileEntry{URL: url, Query: query}, nil
	}

	return seedFileEntry{
		URL:   raw,
		Query: strings.TrimSpace(defaultQuery),
	}, nil
}

func parseSeedKey(raw string) (ed25519.PrivateKey, error) {
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

func parseSeedUFDAmount(value string) (*big.Int, error) {
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

	result := new(big.Int).Mul(whole, big.NewInt(seedUFDScale))
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

func formatSeedUFDAmount(value *big.Int) string {
	if value == nil {
		return "0.0000"
	}

	sign := ""
	amount := new(big.Int).Set(value)
	if amount.Sign() < 0 {
		sign = "-"
		amount.Neg(amount)
	}

	quotient := new(big.Int).Quo(amount, big.NewInt(seedUFDScale))
	remainder := new(big.Int).Mod(amount, big.NewInt(seedUFDScale))
	return fmt.Sprintf("%s%s.%04s", sign, quotient.String(), leftPadSeed(remainder.String(), 4))
}

func leftPadSeed(value string, width int) string {
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

func envOrDefaultInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := parseSeedInt(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envOrDefaultUint64(key string, fallback uint64) uint64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := parseSeedUint(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseSeedInt(value string) (int, error) {
	parsed, err := parseSeedUint(value)
	if err != nil {
		return 0, err
	}
	return int(parsed), nil
}

func parseSeedUint(value string) (uint64, error) {
	parsed, ok := new(big.Int).SetString(strings.TrimSpace(value), 10)
	if !ok || parsed.Sign() < 0 {
		return 0, fmt.Errorf("invalid unsigned integer %q", value)
	}
	return parsed.Uint64(), nil
}

func mustParseBigInt(value string) *big.Int {
	parsed, ok := new(big.Int).SetString(strings.TrimSpace(value), 10)
	if !ok {
		panic("invalid big integer")
	}
	return parsed
}

func isTransientSeedError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	transientFragments := []string{
		"sender pending limit reached",
		"mempool is full",
		"invalid nonce",
		"rate limit exceeded",
	}
	for _, fragment := range transientFragments {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return errors.Is(err, io.EOF)
}
