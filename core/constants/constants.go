package constants

import "math/big"

const (
	GenesisArchitectAddress  = "UFI_GENESIS_ARCHITECT_REPLACE_ME"
	SearchPrecompileAddress  = "0x101"
	UNSRegistryAddress       = "0x102"
	DefaultChainID           = uint64(333)
	GenesisTimestampUnix     = int64(1735689600)
	UNSBasePriceUnits        = int64(100000)
	UNSPopularityPriceUnits  = int64(10000)
	ArchitectFeeNumerator    = int64(333)
	ArchitectFeeDenominator  = int64(10000)
	BasisPoints              = uint64(10000)
	DefaultMultiplierBPS     = BasisPoints
	GovernanceProposalBlocks = uint64(40320)
	GovernanceQuorumBPS      = uint64(1000)
)

func UNSBasePrice() *big.Int {
	return big.NewInt(UNSBasePriceUnits)
}

func UNSPopularityMultiplier() *big.Int {
	return big.NewInt(UNSPopularityPriceUnits)
}

func ArchitectFee(amount *big.Int) *big.Int {
	if amount == nil || amount.Sign() <= 0 {
		return big.NewInt(0)
	}

	fee := new(big.Int).Mul(amount, big.NewInt(ArchitectFeeNumerator))
	return fee.Quo(fee, big.NewInt(ArchitectFeeDenominator))
}

func SplitArchitectFee(amount *big.Int) (net *big.Int, fee *big.Int) {
	total := cloneBigInt(amount)
	fee = ArchitectFee(total)
	net = new(big.Int).Sub(total, fee)
	return net, fee
}

func ApplyBasisPoints(amount *big.Int, bps uint64) *big.Int {
	value := cloneBigInt(amount)
	if value.Sign() <= 0 {
		return big.NewInt(0)
	}

	scaled := new(big.Int).Mul(value, new(big.Int).SetUint64(bps))
	return scaled.Quo(scaled, new(big.Int).SetUint64(BasisPoints))
}

func ScaleUint64Ceil(value, bps uint64) uint64 {
	if value == 0 || bps == 0 {
		return 0
	}

	numerator := new(big.Int).Mul(new(big.Int).SetUint64(value), new(big.Int).SetUint64(bps))
	denominator := new(big.Int).SetUint64(BasisPoints)
	numerator.Add(numerator, new(big.Int).Sub(denominator, big.NewInt(1)))
	numerator.Quo(numerator, denominator)
	if numerator.BitLen() > 64 {
		return ^uint64(0)
	}

	return numerator.Uint64()
}

func ComposeBasisPoints(multipliers ...uint64) uint64 {
	composite := new(big.Int).SetUint64(BasisPoints)
	denominator := new(big.Int).SetUint64(BasisPoints)

	for _, multiplier := range multipliers {
		if multiplier == 0 {
			return 0
		}

		composite.Mul(composite, new(big.Int).SetUint64(multiplier))
		composite.Add(composite, new(big.Int).Sub(denominator, big.NewInt(1)))
		composite.Quo(composite, denominator)
	}

	if composite.BitLen() > 64 {
		return ^uint64(0)
	}

	return composite.Uint64()
}

func cloneBigInt(value *big.Int) *big.Int {
	if value == nil {
		return big.NewInt(0)
	}

	return new(big.Int).Set(value)
}
