package core

import (
	"errors"
	"math/big"
	"strings"

	"unified/core/constants"
)

var (
	ErrUnsupportedNativeContract = errors.New("core: unsupported native contract")
	ErrNativeCallNotPayable      = errors.New("core: native contract does not accept value")
)

type CallMessage struct {
	From  string
	To    string
	Data  []byte
	Value *big.Int
}

func CallNativeContract(state *StateSnapshot, to string, data []byte) ([]byte, error) {
	if state == nil {
		return nil, ErrInvalidTransaction
	}

	contract, ok := systemContractByAddress(to)
	if !ok {
		return nil, ErrUnsupportedNativeContract
	}
	return contract.call(state, CallMessage{To: strings.TrimSpace(to), Data: data})
}

func ExecuteNativeTransfer(state *StateSnapshot, tx Transaction, totalValue, netValue *big.Int) error {
	if state == nil {
		return ErrInvalidTransaction
	}

	contract, ok := systemContractByAddress(tx.To)
	if !ok {
		return ErrUnsupportedNativeContract
	}
	if contract.execute == nil {
		return ErrNativeCallNotPayable
	}
	return contract.execute(state, tx, totalValue, netValue)
}

func ExecuteReadOnlyCall(state *StateSnapshot, call CallMessage) ([]byte, error) {
	if state == nil {
		return nil, ErrInvalidTransaction
	}
	if cloneBigInt(call.Value).Sign() != 0 {
		return nil, ErrNativeCallNotPayable
	}
	return CallNativeContract(state, call.To, call.Data)
}

func UNSRegistrationPriceFromState(state *StateSnapshot, name string) (*big.Int, error) {
	normalized := normalizeUNSName(name)
	if normalized == "" {
		return nil, ErrInvalidTransaction
	}

	base := constants.UNSBasePrice()
	popularity := new(big.Int).Mul(
		new(big.Int).SetUint64(mentionFrequencyFromState(state, normalized)),
		constants.UNSPopularityMultiplier(),
	)
	return new(big.Int).Add(base, popularity), nil
}

func mentionFrequencyFromState(state *StateSnapshot, term string) uint64 {
	if state == nil {
		return 0
	}
	return state.MentionCounts[normalizeTerm(term)]
}

func executeUNSRegistration(state *StateSnapshot, tx Transaction, totalValue, netValue *big.Int) error {
	name, err := DecodeRegisterNameCall(tx.Data)
	if err != nil {
		return err
	}
	normalizedName := normalizeUNSName(name)
	if normalizedName == "" {
		return ErrInvalidTransaction
	}
	if _, exists := state.Names[normalizedName]; exists {
		return ErrInvalidTransaction
	}

	requiredPrice, err := UNSRegistrationPriceFromState(state, normalizedName)
	if err != nil {
		return err
	}
	if cloneBigInt(totalValue).Cmp(requiredPrice) != 0 {
		return ErrInvalidTransaction
	}

	creditBalance(state.Balances, constants.UNSRegistryAddress, netValue)
	state.Names[normalizedName] = NameRecord{
		Name:         normalizedName,
		Owner:        tx.From,
		RegisteredAt: tx.Timestamp,
		TxHash:       tx.Hash,
	}
	return nil
}

func buildNativeReadOnlyCall(to, input string) ([]byte, error) {
	switch strings.TrimSpace(to) {
	case constants.SearchPrecompileAddress:
		return EncodeMentionFrequencyCall(input)
	case constants.UNSRegistryAddress:
		return EncodeRegistrationPriceCall(input)
	default:
		return nil, ErrUnsupportedNativeContract
	}
}
