package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"sort"
	"strings"

	"unified/core/constants"
)

type ContractFunction struct {
	Signature  string `json:"signature"`
	Selector   string `json:"selector"`
	Mutability string `json:"mutability"`
}

type ContractRecord struct {
	Address         string             `json:"address"`
	Name            string             `json:"name"`
	Kind            string             `json:"kind"`
	Handler         string             `json:"handler"`
	Code            string             `json:"code"`
	CodeHash        string             `json:"codeHash"`
	Description     string             `json:"description"`
	System          bool               `json:"system"`
	Executable      bool               `json:"executable"`
	GenesisDeployed bool               `json:"genesisDeployed"`
	DeploymentBlock uint64             `json:"deploymentBlock"`
	DeploymentModel string             `json:"deploymentModel"`
	Functions       []ContractFunction `json:"functions"`
	Source          string             `json:"source"`
	StorageModel    string             `json:"storageModel"`
}

type systemContract struct {
	record  ContractRecord
	call    func(*StateSnapshot, CallMessage) ([]byte, error)
	execute func(*StateSnapshot, Transaction, *big.Int, *big.Int) error
}

func IsNativeContractAddress(address string) bool {
	_, ok := systemContractByAddress(address)
	return ok
}

func SystemContractAt(address string) (ContractRecord, bool) {
	contract, ok := systemContractByAddress(address)
	if !ok {
		return ContractRecord{}, false
	}

	record := contract.record
	record.Functions = append([]ContractFunction(nil), record.Functions...)
	return record, true
}

func ListSystemContracts() []ContractRecord {
	records := make([]ContractRecord, 0, len(protocolSystemContracts))
	for _, contract := range protocolSystemContracts {
		record := contract.record
		record.Functions = append([]ContractFunction(nil), record.Functions...)
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Address < records[j].Address
	})
	return records
}

var protocolSystemContracts = buildProtocolSystemContracts()

func buildProtocolSystemContracts() map[string]systemContract {
	searchFunctions := []ContractFunction{
		{
			Signature:  "mentionFrequency(string)",
			Selector:   selectorHex(mentionFrequencySelector),
			Mutability: "view",
		},
	}
	unsFunctions := []ContractFunction{
		{
			Signature:  "register(string)",
			Selector:   selectorHex(registerSelector),
			Mutability: "nonpayable",
		},
		{
			Signature:  "registerName(string)",
			Selector:   selectorHex(registerNameSelector),
			Mutability: "nonpayable",
		},
		{
			Signature:  "registrationPrice(string)",
			Selector:   selectorHex(registrationPriceSelector),
			Mutability: "view",
		},
		{
			Signature:  "mentionFrequency(string)",
			Selector:   selectorHex(mentionFrequencySelector),
			Mutability: "view",
		},
	}

	searchRecord := buildSystemContractRecord(
		constants.SearchPrecompileAddress,
		"SearchPrecompile",
		"precompile",
		"search-index",
		"System search precompile exposing mention-frequency reads against the local crawl ledger.",
		"protocol://system/search-precompile",
		"derived from search index state",
		false,
		searchFunctions,
	)
	unsRecord := buildSystemContractRecord(
		constants.UNSRegistryAddress,
		"UNSRegistry",
		"system-contract",
		"uns-registry",
		"System UNS registry contract with popularity-based pricing and architect fee enforcement.",
		"protocol://system/uns-registry",
		"derived from names, balances, and mention counts",
		false,
		unsFunctions,
	)

	return map[string]systemContract{
		searchRecord.Address: {
			record: searchRecord,
			call: func(state *StateSnapshot, call CallMessage) ([]byte, error) {
				term, err := DecodeMentionFrequencyCall(call.Data)
				if err != nil {
					return nil, err
				}
				return padUint256(new(big.Int).SetUint64(mentionFrequencyFromState(state, term))), nil
			},
			execute: func(state *StateSnapshot, tx Transaction, totalValue, netValue *big.Int) error {
				return ErrNativeCallNotPayable
			},
		},
		unsRecord.Address: {
			record: unsRecord,
			call: func(state *StateSnapshot, call CallMessage) ([]byte, error) {
				if term, err := DecodeRegistrationPriceCall(call.Data); err == nil {
					price, priceErr := UNSRegistrationPriceFromState(state, term)
					if priceErr != nil {
						return nil, priceErr
					}
					return padUint256(price), nil
				}
				if term, err := DecodeMentionFrequencyCall(call.Data); err == nil {
					return padUint256(new(big.Int).SetUint64(mentionFrequencyFromState(state, term))), nil
				}
				return nil, ErrInvalidUNSCall
			},
			execute: func(state *StateSnapshot, tx Transaction, totalValue, netValue *big.Int) error {
				return executeUNSRegistration(state, tx, totalValue, netValue)
			},
		},
	}
}

func buildSystemContractRecord(address, name, kind, handler, description, source, storageModel string, executable bool, functions []ContractFunction) ContractRecord {
	record := ContractRecord{
		Address:         strings.TrimSpace(address),
		Name:            strings.TrimSpace(name),
		Kind:            strings.TrimSpace(kind),
		Handler:         strings.TrimSpace(handler),
		Description:     strings.TrimSpace(description),
		System:          true,
		Executable:      executable,
		GenesisDeployed: true,
		DeploymentBlock: 0,
		DeploymentModel: "native-protocol-contract",
		Functions:       append([]ContractFunction(nil), functions...),
		Source:          strings.TrimSpace(source),
		StorageModel:    strings.TrimSpace(storageModel),
	}
	record.Code = buildDescriptorCode(record)
	record.CodeHash = hashHex(strings.TrimPrefix(record.Code, "0x"))
	return record
}

func systemContractByAddress(address string) (systemContract, bool) {
	contract, ok := protocolSystemContracts[strings.TrimSpace(address)]
	return contract, ok
}

func buildDescriptorCode(record ContractRecord) string {
	payload := struct {
		Protocol  string             `json:"protocol"`
		Address   string             `json:"address"`
		Name      string             `json:"name"`
		Kind      string             `json:"kind"`
		Handler   string             `json:"handler"`
		Functions []ContractFunction `json:"functions"`
	}{
		Protocol:  "UniFied/SystemContract/v1",
		Address:   record.Address,
		Name:      record.Name,
		Kind:      record.Kind,
		Handler:   record.Handler,
		Functions: record.Functions,
	}
	encoded, _ := json.Marshal(payload)

	// Descriptor bytecode is deterministic metadata for `eth_getCode`/tooling introspection.
	// It is not yet backed by a general-purpose VM execution engine.
	code := append([]byte{0xfe, 0x55, 0x46, 0x49}, encoded...)
	return "0x" + hex.EncodeToString(code)
}

func selectorHex(selector [4]byte) string {
	return "0x" + hex.EncodeToString(selector[:])
}

func hashHex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "0x" + hex.EncodeToString(sum[:])
}
