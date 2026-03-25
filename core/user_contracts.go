package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"strings"
)

const (
	UserContractRuntimeDescriptorV1 = "descriptor-v1"
	UserContractRuntimeNoteV1       = "note-v1"

	maxUserContractCodeBytes = 128 << 10
)

var (
	ErrUnsupportedUserContractRuntime = errors.New("core: unsupported user contract runtime")
	ErrInvalidUserContractCall        = errors.New("core: invalid user contract call")

	noteSelector    = functionSelector("note()")
	setNoteSelector = functionSelector("setNote(string)")
)

func normalizeContractDeploymentRequest(request ContractDeploymentRequest) (ContractDeploymentRequest, error) {
	normalized := request
	normalized.Name = strings.TrimSpace(normalized.Name)
	normalized.Runtime = strings.ToLower(strings.TrimSpace(normalized.Runtime))
	normalized.Description = strings.TrimSpace(normalized.Description)
	normalized.Source = strings.TrimSpace(normalized.Source)
	normalized.StorageModel = strings.TrimSpace(normalized.StorageModel)
	normalized.Code = canonicalHexCode(normalized.Code)
	normalized.Functions = cloneContractFunctions(normalized.Functions)

	if normalized.Name == "" {
		return ContractDeploymentRequest{}, ErrInvalidTransaction
	}
	if normalized.Runtime == "" {
		normalized.Runtime = UserContractRuntimeDescriptorV1
	}
	if normalized.Source == "" {
		normalized.Source = "protocol://user-contract"
	}

	switch normalized.Runtime {
	case UserContractRuntimeDescriptorV1:
		if normalized.StorageModel == "" {
			normalized.StorageModel = "descriptor metadata only"
		}
	case UserContractRuntimeNoteV1:
		if normalized.StorageModel == "" {
			normalized.StorageModel = "single note value"
		}
		if len(normalized.Functions) == 0 {
			normalized.Functions = []ContractFunction{
				{
					Signature:  "note()",
					Selector:   selectorHex(noteSelector),
					Mutability: "view",
				},
				{
					Signature:  "setNote(string)",
					Selector:   selectorHex(setNoteSelector),
					Mutability: "nonpayable",
				},
			}
		}
	default:
		return ContractDeploymentRequest{}, ErrUnsupportedUserContractRuntime
	}

	if normalized.Code != "" {
		if _, err := decodeHexContractCode(normalized.Code); err != nil {
			return ContractDeploymentRequest{}, err
		}
	}

	return normalized, nil
}

func BuildUserContractRecord(tx Transaction, request ContractDeploymentRequest) (ContractRecord, error) {
	normalizedRequest, err := normalizeContractDeploymentRequest(request)
	if err != nil {
		return ContractRecord{}, err
	}

	record := ContractRecord{
		Address:         deriveUserContractAddress(tx.Hash),
		Name:            normalizedRequest.Name,
		Kind:            "user-contract",
		Handler:         normalizedRequest.Runtime,
		Description:     normalizedRequest.Description,
		Owner:           tx.From,
		TxHash:          tx.Hash,
		System:          false,
		Executable:      normalizedRequest.Runtime == UserContractRuntimeNoteV1,
		GenesisDeployed: false,
		DeploymentBlock: 0,
		DeploymentModel: "user-deployed-contract",
		Functions:       cloneContractFunctions(normalizedRequest.Functions),
		Source:          normalizedRequest.Source,
		StorageModel:    normalizedRequest.StorageModel,
	}

	if normalizedRequest.Code != "" {
		record.Code = normalizedRequest.Code
	} else {
		record.Code = buildUserContractDescriptorCode(record)
	}
	record.CodeHash = hashHex(strings.TrimPrefix(record.Code, "0x"))
	return record, nil
}

func ExecuteUserContractReadOnlyCall(state *StateSnapshot, record ContractRecord, call CallMessage) ([]byte, error) {
	switch strings.TrimSpace(record.Handler) {
	case UserContractRuntimeNoteV1:
		if cloneBigInt(call.Value).Sign() != 0 {
			return nil, ErrNativeCallNotPayable
		}
		if !matchesSelector(call.Data, noteSelector) {
			return nil, ErrInvalidUserContractCall
		}
		return encodeABIString(state.ContractData[record.Address]), nil
	case UserContractRuntimeDescriptorV1:
		return nil, ErrUnsupportedUserContractRuntime
	default:
		return nil, ErrUnsupportedUserContractRuntime
	}
}

func ExecuteUserContractTransfer(state *StateSnapshot, record ContractRecord, tx Transaction, totalValue, netValue *big.Int) error {
	switch strings.TrimSpace(record.Handler) {
	case UserContractRuntimeNoteV1:
		if cloneBigInt(totalValue).Sign() != 0 {
			return ErrNativeCallNotPayable
		}
		note, err := decodeSingleStringCall(tx.Data, setNoteSelector)
		if err != nil {
			return err
		}
		state.ContractData[record.Address] = note
		return nil
	case UserContractRuntimeDescriptorV1:
		return ErrUnsupportedUserContractRuntime
	default:
		return ErrUnsupportedUserContractRuntime
	}
}

func deriveUserContractAddress(txHash string) string {
	sum := sha256.Sum256([]byte("ufi-user-contract|" + strings.TrimSpace(txHash)))
	return "0x" + hex.EncodeToString(sum[:20])
}

func buildUserContractDescriptorCode(record ContractRecord) string {
	payload := struct {
		Protocol  string             `json:"protocol"`
		Address   string             `json:"address"`
		Name      string             `json:"name"`
		Runtime   string             `json:"runtime"`
		Owner     string             `json:"owner"`
		Functions []ContractFunction `json:"functions"`
	}{
		Protocol:  "UniFied/UserContract/v1",
		Address:   record.Address,
		Name:      record.Name,
		Runtime:   record.Handler,
		Owner:     record.Owner,
		Functions: cloneContractFunctions(record.Functions),
	}
	encoded, _ := json.Marshal(payload)
	code := append([]byte{0xfe, 0x55, 0x43, 0x54}, encoded...)
	return "0x" + hex.EncodeToString(code)
}

func canonicalHexCode(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return ""
	}
	if strings.HasPrefix(cleaned, "0x") || strings.HasPrefix(cleaned, "0X") {
		return "0x" + strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(cleaned, "0x"), "0X"))
	}
	return "0x" + strings.ToLower(cleaned)
}

func decodeHexContractCode(raw string) ([]byte, error) {
	cleaned := strings.TrimSpace(strings.TrimPrefix(canonicalHexCode(raw), "0x"))
	if cleaned == "" {
		return nil, ErrInvalidTransaction
	}
	if len(cleaned)%2 != 0 {
		return nil, ErrInvalidTransaction
	}
	decoded, err := hex.DecodeString(cleaned)
	if err != nil {
		return nil, ErrInvalidTransaction
	}
	if len(decoded) > maxUserContractCodeBytes {
		return nil, ErrInvalidTransaction
	}
	return decoded, nil
}

func encodeABIString(value string) []byte {
	payload := []byte(strings.TrimSpace(value))
	paddedLength := ((len(payload) + 31) / 32) * 32
	encoded := make([]byte, 64+paddedLength)
	binary.BigEndian.PutUint64(encoded[24:32], 32)
	binary.BigEndian.PutUint64(encoded[56:64], uint64(len(payload)))
	copy(encoded[64:], payload)
	return encoded
}

func matchesSelector(data []byte, selector [4]byte) bool {
	return len(data) == 4 && bytes.Equal(data[:4], selector[:])
}

func cloneContractFunctions(functions []ContractFunction) []ContractFunction {
	out := make([]ContractFunction, 0, len(functions))
	for _, fn := range functions {
		copyFn := fn
		copyFn.Signature = strings.TrimSpace(copyFn.Signature)
		copyFn.Selector = strings.TrimSpace(copyFn.Selector)
		copyFn.Mutability = strings.TrimSpace(copyFn.Mutability)
		out = append(out, copyFn)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Selector < out[j].Selector
	})
	return out
}
