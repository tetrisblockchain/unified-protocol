package core

import (
	"encoding/binary"
	"errors"
	"strings"

	"golang.org/x/crypto/sha3"
)

var (
	ErrInvalidUNSCall = errors.New("core: invalid UNS contract call")

	registerNameSelector      = functionSelector("registerName(string)")
	registerSelector          = functionSelector("register(string)")
	registrationPriceSelector = functionSelector("registrationPrice(string)")
	mentionFrequencySelector  = functionSelector("mentionFrequency(string)")
)

func EncodeRegisterNameCall(name string) ([]byte, error) {
	return encodeSingleStringCall(registerNameSelector, name)
}

func EncodeRegistrationPriceCall(name string) ([]byte, error) {
	return encodeSingleStringCall(registrationPriceSelector, name)
}

func EncodeMentionFrequencyCall(term string) ([]byte, error) {
	return encodeSingleStringCall(mentionFrequencySelector, term)
}

func DecodeRegisterNameCall(data []byte) (string, error) {
	return decodeSingleStringCall(data, registerNameSelector, registerSelector)
}

func DecodeRegistrationPriceCall(data []byte) (string, error) {
	return decodeSingleStringCall(data, registrationPriceSelector)
}

func DecodeMentionFrequencyCall(data []byte) (string, error) {
	return decodeSingleStringCall(data, mentionFrequencySelector)
}

func encodeSingleStringCall(selector [4]byte, value string) ([]byte, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return nil, ErrInvalidUNSCall
	}

	stringData := []byte(normalized)
	paddedLength := ((len(stringData) + 31) / 32) * 32
	payload := make([]byte, 4+32+32+paddedLength)
	copy(payload[:4], selector[:])
	binary.BigEndian.PutUint64(payload[4+24:4+32], 32)
	binary.BigEndian.PutUint64(payload[4+32+24:4+64], uint64(len(stringData)))
	copy(payload[4+64:], stringData)
	return payload, nil
}

func decodeSingleStringCall(data []byte, selectors ...[4]byte) (string, error) {
	if len(data) < 4+64 {
		return "", ErrInvalidUNSCall
	}
	selector := data[:4]
	matched := false
	for _, candidate := range selectors {
		if string(selector) == string(candidate[:]) {
			matched = true
			break
		}
	}
	if !matched {
		return "", ErrInvalidUNSCall
	}

	offset := binary.BigEndian.Uint64(data[4+24 : 4+32])
	if offset != 32 {
		return "", ErrInvalidUNSCall
	}

	length := binary.BigEndian.Uint64(data[4+32+24 : 4+64])
	if length == 0 {
		return "", ErrInvalidUNSCall
	}

	start := 4 + 64
	end := start + int(length)
	if end > len(data) {
		return "", ErrInvalidUNSCall
	}

	name := strings.TrimSpace(string(data[start:end]))
	if name == "" {
		return "", ErrInvalidUNSCall
	}
	return name, nil
}

func functionSelector(signature string) [4]byte {
	var selector [4]byte
	hasher := sha3.NewLegacyKeccak256()
	_, _ = hasher.Write([]byte(signature))
	sum := hasher.Sum(nil)
	copy(selector[:], sum[:4])
	return selector
}
