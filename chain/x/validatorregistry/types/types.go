package types

// LockStartHeight normalises the block height at which a validator's self-stake
// lock-up window begins, for storage in ValidatorMetadata.BondingStartHeight.
//
// It never returns a value below 1. Zero is not a usable lock start: it is both
// the proto3 default for an unset field AND the height reported during InitChain
// (baseapp treats the init-chain block height as 0 even though InitialHeight is
// 1), so a stored 0 cannot be told apart from "never recorded".
//
// That ambiguity is not cosmetic. The V-3 invariant flags a self-stake exit whose
// entry was created at or after this height; with 0 the comparison is true for
// EVERY entry, turning a fail-loud check into a network-wide panic primitive that
// a founding validator would carry from genesis. Clamping to 1 makes the stored
// value mean what it says — "the lock-up began at the first block of this chain".
func LockStartHeight(blockHeight int64) int64 {
	if blockHeight < 1 {
		return 1
	}
	return blockHeight
}
