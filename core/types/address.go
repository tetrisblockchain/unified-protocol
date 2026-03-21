package types

import (
	"context"
	"crypto/sha256"
	"errors"
	"math/big"
	"strings"

	"golang.org/x/crypto/ripemd160"
)

const (
	UFIPrefix             = "UFI"
	CurrentAddressVersion = byte(0x01)
	addressHashSize       = 20
	addressChecksumSize   = 4
)

var (
	ErrAliasResolverRequired = errors.New("types: alias resolver is required")
	ErrEmptyPublicKey        = errors.New("types: public key is required")
	ErrInvalidAddress        = errors.New("types: invalid UFI address")
	ErrInvalidChecksum       = errors.New("types: invalid UFI checksum")
)

var (
	base58Alphabet  = []byte("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz")
	base58DecodeMap = buildBase58DecodeMap()
)

// AliasResolver allows address parsing to fall back to UNS aliases natively.
type AliasResolver interface {
	ResolveAlias(ctx context.Context, alias string) (Address, error)
	ReverseResolve(ctx context.Context, address Address) (string, error)
}

// Address is the canonical Base58Check-backed UFI account identifier.
type Address struct {
	version byte
	hash160 [addressHashSize]byte
}

func NewAddress(version byte, hash []byte) (Address, error) {
	if version == 0 || len(hash) != addressHashSize {
		return Address{}, ErrInvalidAddress
	}

	var payload [addressHashSize]byte
	copy(payload[:], hash)

	return Address{
		version: version,
		hash160: payload,
	}, nil
}

func NewAddressFromPubKey(pubKey []byte) (Address, error) {
	if len(pubKey) == 0 {
		return Address{}, ErrEmptyPublicKey
	}

	shaHash := sha256.Sum256(pubKey)
	ripemd := ripemd160.New()
	if _, err := ripemd.Write(shaHash[:]); err != nil {
		return Address{}, err
	}

	return NewAddress(CurrentAddressVersion, ripemd.Sum(nil))
}

func ParseAddress(encoded string) (Address, error) {
	value := strings.TrimSpace(encoded)
	if !strings.HasPrefix(value, UFIPrefix) {
		return Address{}, ErrInvalidAddress
	}

	decoded, err := base58Decode(strings.TrimPrefix(value, UFIPrefix))
	if err != nil {
		return Address{}, err
	}

	if len(decoded) != 1+addressHashSize+addressChecksumSize {
		return Address{}, ErrInvalidAddress
	}

	version := decoded[0]
	if version == 0 {
		return Address{}, ErrInvalidAddress
	}

	var hash160 [addressHashSize]byte
	copy(hash160[:], decoded[1:1+addressHashSize])

	expected := addressChecksum(version, hash160)
	if !checksumMatches(expected[:], decoded[1+addressHashSize:]) {
		return Address{}, ErrInvalidChecksum
	}

	return Address{
		version: version,
		hash160: hash160,
	}, nil
}

func ResolveAddress(ctx context.Context, input string, resolver AliasResolver) (Address, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	value := strings.TrimSpace(input)
	if value == "" {
		return Address{}, ErrInvalidAddress
	}

	if strings.HasPrefix(value, UFIPrefix) {
		return ParseAddress(value)
	}

	if resolver == nil {
		return Address{}, ErrAliasResolverRequired
	}

	return resolver.ResolveAlias(ctx, NormalizeAlias(value))
}

func LookupAlias(ctx context.Context, address Address, resolver AliasResolver) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if resolver == nil {
		return "", ErrAliasResolverRequired
	}

	return resolver.ReverseResolve(ctx, address)
}

func NormalizeAlias(alias string) string {
	return strings.ToLower(strings.TrimSpace(alias))
}

func (a Address) Bytes() []byte {
	if a.IsZero() {
		return nil
	}

	raw := make([]byte, 1+addressHashSize)
	raw[0] = a.version
	copy(raw[1:], a.hash160[:])
	return raw
}

func (a Address) Equal(other Address) bool {
	return a.version == other.version && a.hash160 == other.hash160
}

func (a Address) Hash160() [addressHashSize]byte {
	return a.hash160
}

func (a Address) IsZero() bool {
	return a.version == 0 && a.hash160 == [addressHashSize]byte{}
}

func (a Address) MarshalText() ([]byte, error) {
	return []byte(a.String()), nil
}

func (a Address) String() string {
	if a.IsZero() {
		return ""
	}

	raw := make([]byte, 0, 1+addressHashSize+addressChecksumSize)
	raw = append(raw, a.version)
	raw = append(raw, a.hash160[:]...)

	checksum := addressChecksum(a.version, a.hash160)
	raw = append(raw, checksum[:]...)

	return UFIPrefix + base58Encode(raw)
}

func (a *Address) UnmarshalText(text []byte) error {
	parsed, err := ParseAddress(string(text))
	if err != nil {
		return err
	}

	*a = parsed
	return nil
}

func (a Address) Version() byte {
	return a.version
}

func addressChecksum(version byte, hash160 [addressHashSize]byte) [addressChecksumSize]byte {
	payload := make([]byte, 0, len(UFIPrefix)+1+addressHashSize)
	payload = append(payload, UFIPrefix...)
	payload = append(payload, version)
	payload = append(payload, hash160[:]...)

	first := sha256.Sum256(payload)
	second := sha256.Sum256(first[:])

	var checksum [addressChecksumSize]byte
	copy(checksum[:], second[:addressChecksumSize])
	return checksum
}

func base58Decode(input string) ([]byte, error) {
	if input == "" {
		return nil, ErrInvalidAddress
	}

	value := big.NewInt(0)
	base := big.NewInt(58)

	for i := 0; i < len(input); i++ {
		index := base58DecodeMap[input[i]]
		if index < 0 {
			return nil, ErrInvalidAddress
		}

		value.Mul(value, base)
		value.Add(value, big.NewInt(int64(index)))
	}

	decoded := value.Bytes()
	leadingZeros := 0
	for leadingZeros < len(input) && input[leadingZeros] == base58Alphabet[0] {
		leadingZeros++
	}

	out := make([]byte, leadingZeros+len(decoded))
	copy(out[leadingZeros:], decoded)
	return out, nil
}

func base58Encode(input []byte) string {
	if len(input) == 0 {
		return ""
	}

	value := new(big.Int).SetBytes(input)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	mod := new(big.Int)
	encoded := make([]byte, 0, len(input)*2)

	for value.Cmp(zero) > 0 {
		value.DivMod(value, base, mod)
		encoded = append(encoded, base58Alphabet[mod.Int64()])
	}

	for _, b := range input {
		if b != 0 {
			break
		}
		encoded = append(encoded, base58Alphabet[0])
	}

	reverseBytes(encoded)
	return string(encoded)
}

func buildBase58DecodeMap() [256]int {
	var lookup [256]int
	for i := range lookup {
		lookup[i] = -1
	}

	for i, ch := range base58Alphabet {
		lookup[ch] = i
	}

	return lookup
}

func checksumMatches(expected, actual []byte) bool {
	if len(expected) != len(actual) {
		return false
	}

	for i := range expected {
		if expected[i] != actual[i] {
			return false
		}
	}

	return true
}

func reverseBytes(data []byte) {
	for left, right := 0, len(data)-1; left < right; left, right = left+1, right-1 {
		data[left], data[right] = data[right], data[left]
	}
}
