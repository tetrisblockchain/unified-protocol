package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"unified/core"
)

type RPCServer struct {
	Blockchain *core.Blockchain
	Engine     *core.Engine
	Logger     *log.Logger
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

type getBlockParams struct {
	Number string `json:"number"`
}

type getSearchDataParams struct {
	URL  string `json:"url"`
	Term string `json:"term"`
}

type precompileParams struct {
	Term string `json:"term"`
}

func NewRPCServer(blockchain *core.Blockchain, engine *core.Engine, logger *log.Logger) *RPCServer {
	if logger == nil {
		logger = log.Default()
	}
	return &RPCServer{
		Blockchain: blockchain,
		Engine:     engine,
		Logger:     logger,
	}
}

func (s *RPCServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		s.writeError(writer, nil, http.StatusMethodNotAllowed, -32600, "method not allowed")
		return
	}

	var rpcPayload rpcRequest
	if err := json.NewDecoder(request.Body).Decode(&rpcPayload); err != nil {
		s.writeError(writer, nil, http.StatusBadRequest, -32700, "invalid JSON-RPC payload")
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
	encoded := strings.TrimSpace(strings.TrimPrefix(raw, "0x"))
	if encoded == "" {
		return core.Transaction{}, fmt.Errorf("missing raw transaction payload")
	}
	bytes, err := hex.DecodeString(encoded)
	if err != nil {
		return core.Transaction{}, err
	}
	var tx core.Transaction
	if err := json.Unmarshal(bytes, &tx); err != nil {
		return core.Transaction{}, err
	}
	return tx, nil
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
