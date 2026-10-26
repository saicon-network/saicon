package types

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/types/bech32"
)

// DecodeAccount returns the bytes of a saicon1… address and rejects any other prefix.
func DecodeAccount(addr string) ([]byte, error) { return decodeBech32(addr, AccountPrefix) }

// DecodeValidator returns the bytes of a saiconvaloper1… address.
func DecodeValidator(addr string) ([]byte, error) { return decodeBech32(addr, ValidatorPrefix) }

func decodeBech32(addr, want string) ([]byte, error) {
	hrp, bz, err := bech32.DecodeAndConvert(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if hrp != want {
		return nil, fmt.Errorf("address %q has prefix %q, want %q", addr, hrp, want)
	}
	if len(bz) == 0 {
		return nil, fmt.Errorf("address %q is empty", addr)
	}
	return bz, nil
}
