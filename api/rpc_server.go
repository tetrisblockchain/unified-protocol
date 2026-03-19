package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"unified/core"
)

const (
	MaxRPCBodyBytes       = 1 << 20
	DefaultRPCWriteLimit  = 60
	DefaultRPCWriteWindow = time.Minute
)

type RPCServer struct {
	Blockchain *core.Blockchain
	Engine     *core.Engine
	Logger     *log.Logger
	limiter    *clientWindowLimiter
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      any             `json:"id"`
}

type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type submitSearchTaskParams struct {
	Transaction core.Transaction       `json:"transaction"`
	Task        core.SearchTaskRequest `json:"task"`
}

type sendRawTransactionParams struct {
	Raw string `json:"raw"`
}

type getBalanceParams struct {
	Address string `json:"address"`
}

type getTransactionCountParams struct {
	Address string `json:"address"`
	Block   string `json:"block,omitempty"`
}

type getCodeParams struct {
	Address string `json:"address"`
	Block   string `json:"block,omitempty"`
}

type getContractParams struct {
	Address string `json:"address"`
}

type getBlockParams struct {
	Number string `json:"number"`
}

type getBlockByHashParams struct {
	Hash string `json:"hash"`
}

type getSearchDataParams struct {
	URL  string `json:"url"`
	Term string `json:"term"`
}

type searchIndexParams struct {
	Term  string `json:"term"`
	URL   string `json:"url,omitempty"`
	Limit uint64 `json:"limit,omitempty"`
}

type precompileParams struct {
	Term string `json:"term"`
}

type callNativeParams struct {
	To   string `json:"to"`
	Data string `json:"data"`
}

type getNamePriceParams struct {
	Name string `json:"name"`
}

type resolveNameParams struct {
	Name string `json:"name"`
}

type reverseResolveParams struct {
	Address string `json:"address"`
}

type getRecentActivityParams struct {
	Limit uint64 `json:"limit,omitempty"`
}

type getAddressActivityParams struct {
	Address string `json:"address"`
	Limit   uint64 `json:"limit,omitempty"`
}

type getTransactionByHashParams struct {
	Hash string `json:"hash"`
}

type getCrawlProofParams struct {
	TaskTxHash string `json:"taskTxHash,omitempty"`
	TaskID     string `json:"taskId,omitempty"`
}

type getPendingPoolParams struct {
	Address string `json:"address,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type callParams struct {
	To    string `json:"to"`
	From  string `json:"from,omitempty"`
	Data  string `json:"data"`
	Value string `json:"value,omitempty"`
	Block string `json:"block,omitempty"`
}

func NewRPCServer(blockchain *core.Blockchain, engine *core.Engine, logger *log.Logger) *RPCServer {
	if logger == nil {
		logger = log.Default()
	}
	return &RPCServer{
		Blockchain: blockchain,
		Engine:     engine,
		Logger:     logger,
		limiter:    newClientWindowLimiter(DefaultRPCWriteLimit, DefaultRPCWriteWindow),
	}
}

func (s *RPCServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		s.writeError(writer, nil, http.StatusMethodNotAllowed, -32600, "method not allowed")
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, MaxRPCBodyBytes)
	var rpcPayload rpcRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rpcPayload); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			s.writeError(writer, nil, http.StatusRequestEntityTooLarge, -32001, "request body too large")
			return
		}
		s.writeError(writer, nil, http.StatusBadRequest, -32700, "invalid JSON-RPC payload")
		return
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		s.writeError(writer, nil, http.StatusBadRequest, -32700, "invalid JSON-RPC payload")
		return
	}
	if s.isMutatingMethod(rpcPayload.Method) && s.limiter != nil && !s.limiter.Allow(clientAddress(request), time.Now()) {
		s.writeError(writer, rpcPayload.ID, http.StatusTooManyRequests, -32005, "rate limit exceeded")
		return
	}

	result, rpcErr := s.handle(rpcPayload)
	statusCode := http.StatusOK
	if rpcErr != nil {
		statusCode = http.StatusBadRequest
	}
	s.writeResponse(writer, statusCode, response{
		JSONRPC: "2.0",
		ID:      rpcPayload.ID,
		Result:  result,
		Error:   rpcErr,
	})
}

func (s *RPCServer) handle(r rpcRequest) (any, *rpcError) {
	switch r.Method {
	case "ufi_getBalance":
		var params getBalanceParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return map[string]string{"balance": s.Blockchain.GetBalance(params.Address).String()}, nil
	case "ufi_getTransactionCount":
		address, blockRef, err := decodeTransactionCountParams(r.Params)
		if err != nil {
			return nil, invalidParams(err)
		}
		nonce, err := s.resolveTransactionCount(address, blockRef)
		if err != nil {
			return nil, rpcFailure(err)
		}
		return map[string]string{"nonce": strconv.FormatUint(nonce, 10)}, nil
	case "eth_getTransactionCount":
		address, blockRef, err := decodeTransactionCountParams(r.Params)
		if err != nil {
			return nil, invalidParams(err)
		}
		nonce, err := s.resolveTransactionCount(address, blockRef)
		if err != nil {
			return nil, rpcFailure(err)
		}
		return fmt.Sprintf("0x%x", nonce), nil
	case "ufi_getNetworkConfig":
		return s.Blockchain.NetworkConfig(), nil
	case "eth_chainId":
		return fmt.Sprintf("0x%x", s.Blockchain.NetworkConfig().ChainID), nil
	case "ufi_getContract":
		var params getContractParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		record, ok := s.Blockchain.ContractAt(params.Address)
		if !ok {
			return nil, rpcFailure(core.ErrUnsupportedNativeContract)
		}
		return record, nil
	case "ufi_listContracts":
		return s.Blockchain.ListContracts(), nil
	case "eth_getCode":
		address, blockRef, err := decodeCodeParams(r.Params)
		if err != nil {
			return nil, invalidParams(err)
		}
		_ = blockRef
		return s.Blockchain.ContractCodeAt(address), nil
	case "ufi_resolveName":
		var params resolveNameParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		record, ok := s.Blockchain.ResolveName(params.Name)
		if !ok {
			return map[string]any{"found": false}, nil
		}
		return map[string]any{
			"found":   true,
			"name":    record.Name,
			"owner":   record.Owner,
			"txHash":  record.TxHash,
			"created": record.RegisteredAt,
		}, nil
	case "ufi_reverseResolve":
		var params reverseResolveParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		record, ok := s.Blockchain.ReverseResolve(params.Address)
		if !ok {
			return map[string]any{"found": false}, nil
		}
		return map[string]any{
			"found":   true,
			"name":    record.Name,
			"owner":   record.Owner,
			"txHash":  record.TxHash,
			"created": record.RegisteredAt,
		}, nil
	case "ufi_sendTransaction":
		var tx core.Transaction
		if err := decodeParams(r.Params, &tx); err != nil {
			return nil, invalidParams(err)
		}
		hash, err := s.Engine.SubmitTransaction(tx)
		if err != nil {
			return nil, rpcFailure(err)
		}
		return map[string]string{"hash": hash}, nil
	case "ufi_sendRawTransaction":
		var params sendRawTransactionParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		tx, err := decodeRawTransaction(params.Raw)
		if err != nil {
			return nil, invalidParams(err)
		}
		if tx.Type == core.TxTypeSearchTask {
			request, err := tx.SearchTaskRequest()
			if err != nil {
				return nil, rpcFailure(err)
			}
			envelope, err := s.Engine.SubmitSearchTask(tx, request)
			if err != nil {
				return nil, rpcFailure(err)
			}
			return map[string]any{
				"hash":        envelope.Transaction.Hash,
				"taskId":      envelope.Task.ID,
				"grossBounty": envelope.Transaction.Value,
			}, nil
		}
		hash, err := s.Engine.SubmitTransaction(tx)
		if err != nil {
			return nil, rpcFailure(err)
		}
		return map[string]string{"hash": hash}, nil
	case "ufi_getBlockByNumber":
		var params getBlockParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		number, err := parseBlockNumber(params.Number, s.Blockchain.LatestBlock().Header.Number)
		if err != nil {
			return nil, invalidParams(err)
		}
		block, err := s.Blockchain.GetBlockByNumber(number)
		if err != nil {
			return nil, rpcFailure(err)
		}
		return block, nil
	case "ufi_getBlockByHash":
		var params getBlockByHashParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		block, err := s.Blockchain.GetBlockByHash(strings.TrimSpace(params.Hash))
		if err != nil {
			return nil, rpcFailure(err)
		}
		return block, nil
	case "ufi_getRecentActivity":
		var params getRecentActivityParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.Blockchain.RecentActivity(params.Limit), nil
	case "ufi_getAddressActivity":
		var params getAddressActivityParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		activity, err := s.Blockchain.AddressActivity(params.Address, params.Limit)
		if err != nil {
			return nil, invalidParams(err)
		}
		return activity, nil
	case "ufi_getTransactionByHash":
		var params getTransactionByHashParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		transaction, err := s.Blockchain.TransactionByHash(params.Hash)
		if err != nil {
			return nil, rpcFailure(err)
		}
		return transaction, nil
	case "ufi_getCrawlProof":
		var params getCrawlProofParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		proof, err := s.Blockchain.CrawlProofByTask(params.TaskTxHash, params.TaskID)
		if err != nil {
			return nil, rpcFailure(err)
		}
		return proof, nil
	case "ufi_getMempoolStatus":
		if s.Engine == nil {
			return nil, rpcFailure(errors.New("mempool is unavailable"))
		}
		return s.Engine.MempoolStatus(), nil
	case "ufi_getPendingTransactions":
		if s.Engine == nil {
			return nil, rpcFailure(errors.New("mempool is unavailable"))
		}
		var params getPendingPoolParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.Engine.PendingTransactions(params.Address, params.Limit), nil
	case "ufi_getPendingSearchTasks":
		if s.Engine == nil {
			return nil, rpcFailure(errors.New("mempool is unavailable"))
		}
		var params getPendingPoolParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.Engine.PendingSearchTasks(params.Address, params.Limit), nil
	case "ufi_submitSearchTask":
		var params submitSearchTaskParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		envelope, err := s.Engine.SubmitSearchTask(params.Transaction, params.Task)
		if err != nil {
			return nil, rpcFailure(err)
		}
		return map[string]any{
			"hash":        envelope.Transaction.Hash,
			"taskId":      envelope.Task.ID,
			"grossBounty": envelope.Transaction.Value,
			"difficulty":  envelope.Task.Difficulty,
			"query":       envelope.Request.Query,
			"url":         envelope.Request.URL,
		}, nil
	case "ufi_getSearchData":
		var params getSearchDataParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.Blockchain.GetSearchData(params.URL, params.Term), nil
	case "ufi_searchIndex":
		var params searchIndexParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		return s.Blockchain.SearchIndex(params.Term, params.URL, params.Limit), nil
	case "ufi_getNamePrice":
		var params getNamePriceParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		price, err := s.Blockchain.UNSRegistrationPrice(params.Name)
		if err != nil {
			return nil, rpcFailure(err)
		}
		return map[string]string{"price": price.String()}, nil
	case "ufi_call", "eth_call":
		call, blockRef, err := decodeCallParams(r.Params)
		if err != nil {
			return nil, invalidParams(err)
		}
		output, err := s.Blockchain.CallContract(call, blockRef)
		if err != nil {
			return nil, rpcFailure(err)
		}
		return fmt.Sprintf("0x%x", output), nil
	case "ufi_callNative":
		var params callNativeParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		input, err := decodeHexPayload(params.Data)
		if err != nil {
			return nil, invalidParams(err)
		}
		output, err := s.Blockchain.CallContract(core.CallMessage{
			To:   params.To,
			Data: input,
		}, "latest")
		if err != nil {
			return nil, rpcFailure(err)
		}
		return map[string]string{"output": fmt.Sprintf("0x%x", output)}, nil
	case "ufi_callPrecompile0101":
		var params precompileParams
		if err := decodeParams(r.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
		output, err := s.Blockchain.PrecompileMentionFrequency(params.Term)
		if err != nil {
			return nil, rpcFailure(err)
		}
		return map[string]string{"output": fmt.Sprintf("0x%x", output)}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func (s *RPCServer) isMutatingMethod(method string) bool {
	switch strings.TrimSpace(method) {
	case "ufi_sendTransaction", "ufi_sendRawTransaction", "ufi_submitSearchTask":
		return true
	default:
		return false
	}
}

type clientWindowLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	clients map[string]clientWindow
}

type clientWindow struct {
	start time.Time
	count int
}

func newClientWindowLimiter(limit int, window time.Duration) *clientWindowLimiter {
	return &clientWindowLimiter{
		limit:   limit,
		window:  window,
		clients: make(map[string]clientWindow),
	}
}

func (l *clientWindowLimiter) Allow(key string, now time.Time) bool {
	if l == nil || l.limit <= 0 || l.window <= 0 {
		return true
	}
	if strings.TrimSpace(key) == "" {
		key = "unknown"
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.clients[key]
	if !ok || now.Sub(entry.start) >= l.window {
		l.clients[key] = clientWindow{start: now, count: 1}
		l.pruneLocked(now)
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	l.clients[key] = entry
	return true
}

func (l *clientWindowLimiter) pruneLocked(now time.Time) {
	for key, entry := range l.clients {
		if now.Sub(entry.start) >= l.window {
			delete(l.clients, key)
		}
	}
}

func clientAddress(request *http.Request) string {
	if request == nil {
		return "unknown"
	}
	if forwarded := strings.TrimSpace(request.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if cleaned := strings.TrimSpace(parts[0]); cleaned != "" {
			return cleaned
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(request.RemoteAddr))
	if err != nil {
		if cleaned := strings.TrimSpace(request.RemoteAddr); cleaned != "" {
			return cleaned
		}
		return "unknown"
	}
	return host
}

func decodeParams(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("missing params")
	}
	if raw[0] == '[' {
		var single []json.RawMessage
		if err := json.Unmarshal(raw, &single); err != nil {
			return err
		}
		if len(single) == 0 {
			return fmt.Errorf("missing params")
		}
		return json.Unmarshal(single[0], out)
	}
	return json.Unmarshal(raw, out)
}

func decodeRawTransaction(raw string) (core.Transaction, error) {
	bytes, err := decodeHexPayload(raw)
	if err != nil {
		return core.Transaction{}, err
	}
	var tx core.Transaction
	if err := json.Unmarshal(bytes, &tx); err != nil {
		return core.Transaction{}, err
	}
	return tx, nil
}

func decodeHexPayload(raw string) ([]byte, error) {
	encoded := strings.TrimSpace(strings.TrimPrefix(raw, "0x"))
	if encoded == "" {
		return nil, fmt.Errorf("missing hex payload")
	}
	return hex.DecodeString(encoded)
}

func decodeCallParams(raw json.RawMessage) (core.CallMessage, string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return core.CallMessage{}, "", fmt.Errorf("missing params")
	}

	var params callParams
	blockRef := "latest"
	if raw[0] == '[' {
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return core.CallMessage{}, "", err
		}
		if len(values) == 0 {
			return core.CallMessage{}, "", fmt.Errorf("missing params")
		}
		if err := json.Unmarshal(values[0], &params); err != nil {
			return core.CallMessage{}, "", err
		}
		if len(values) > 1 && len(values[1]) > 0 && string(values[1]) != "null" {
			if err := json.Unmarshal(values[1], &blockRef); err != nil {
				return core.CallMessage{}, "", err
			}
		}
	} else {
		if err := json.Unmarshal(raw, &params); err != nil {
			return core.CallMessage{}, "", err
		}
		if strings.TrimSpace(params.Block) != "" {
			blockRef = params.Block
		}
	}

	input, err := decodeHexPayload(params.Data)
	if err != nil {
		return core.CallMessage{}, "", err
	}
	value, err := parseCallValue(params.Value)
	if err != nil {
		return core.CallMessage{}, "", err
	}
	return core.CallMessage{
		From:  strings.TrimSpace(params.From),
		To:    strings.TrimSpace(params.To),
		Data:  input,
		Value: value,
	}, blockRef, nil
}

func decodeTransactionCountParams(raw json.RawMessage) (string, string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", fmt.Errorf("missing params")
	}

	params := getTransactionCountParams{Block: "latest"}
	if raw[0] == '[' {
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return "", "", err
		}
		if len(values) == 0 {
			return "", "", fmt.Errorf("missing params")
		}
		if err := json.Unmarshal(values[0], &params.Address); err != nil {
			return "", "", err
		}
		if len(values) > 1 && len(values[1]) > 0 && string(values[1]) != "null" {
			if err := json.Unmarshal(values[1], &params.Block); err != nil {
				return "", "", err
			}
		}
	} else {
		if err := json.Unmarshal(raw, &params); err != nil {
			return "", "", err
		}
	}

	address := strings.TrimSpace(params.Address)
	if address == "" {
		return "", "", fmt.Errorf("address is required")
	}
	blockRef := strings.TrimSpace(params.Block)
	if blockRef == "" {
		blockRef = "latest"
	}
	return address, blockRef, nil
}

func decodeCodeParams(raw json.RawMessage) (string, string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", fmt.Errorf("missing params")
	}

	params := getCodeParams{Block: "latest"}
	if raw[0] == '[' {
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return "", "", err
		}
		if len(values) == 0 {
			return "", "", fmt.Errorf("missing params")
		}
		if err := json.Unmarshal(values[0], &params.Address); err != nil {
			return "", "", err
		}
		if len(values) > 1 && len(values[1]) > 0 && string(values[1]) != "null" {
			if err := json.Unmarshal(values[1], &params.Block); err != nil {
				return "", "", err
			}
		}
	} else {
		if err := json.Unmarshal(raw, &params); err != nil {
			return "", "", err
		}
	}

	address := strings.TrimSpace(params.Address)
	if address == "" {
		return "", "", fmt.Errorf("address is required")
	}
	blockRef := strings.TrimSpace(params.Block)
	if blockRef == "" {
		blockRef = "latest"
	}
	return address, blockRef, nil
}

func (s *RPCServer) resolveTransactionCount(address, blockRef string) (uint64, error) {
	switch strings.ToLower(strings.TrimSpace(blockRef)) {
	case "", "latest":
		return s.Blockchain.NonceAt(address, "latest")
	case "pending":
		if s.Engine == nil {
			return s.Blockchain.NonceAt(address, "latest")
		}
		return s.Engine.PendingNonce(address)
	default:
		return s.Blockchain.NonceAt(address, blockRef)
	}
}

func parseCallValue(raw string) (*big.Int, error) {
	cleaned := strings.TrimSpace(strings.ToLower(raw))
	if cleaned == "" {
		return big.NewInt(0), nil
	}
	if strings.HasPrefix(cleaned, "0x") {
		value, ok := new(big.Int).SetString(strings.TrimPrefix(cleaned, "0x"), 16)
		if !ok {
			return nil, fmt.Errorf("invalid call value")
		}
		return value, nil
	}
	value, ok := new(big.Int).SetString(cleaned, 10)
	if !ok {
		return nil, fmt.Errorf("invalid call value")
	}
	return value, nil
}

func parseBlockNumber(value string, latest uint64) (uint64, error) {
	cleaned := strings.TrimSpace(strings.ToLower(value))
	if cleaned == "" || cleaned == "latest" {
		return latest, nil
	}
	return strconv.ParseUint(cleaned, 10, 64)
}

func invalidParams(err error) *rpcError {
	return &rpcError{Code: -32602, Message: err.Error()}
}

func rpcFailure(err error) *rpcError {
	return &rpcError{Code: -32000, Message: err.Error()}
}

func (s *RPCServer) writeError(writer http.ResponseWriter, id any, statusCode int, code int, message string) {
	s.writeResponse(writer, statusCode, response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	})
}

func (s *RPCServer) writeResponse(writer http.ResponseWriter, statusCode int, payload response) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(payload)
}
