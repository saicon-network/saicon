package app

import sdk "github.com/cosmos/cosmos-sdk/types"

func init() {
	// Set bond denom — base (indivisible) unit is "nscn"; the display unit "SCN"
	// is exposed via bank DenomMetadata at exponent 8 (1 SCN = 100_000_000 nscn).
	sdk.DefaultBondDenom = "nscn"

	// Set address prefixes
	accountPubKeyPrefix := AccountAddressPrefix + "pub"
	validatorAddressPrefix := AccountAddressPrefix + "valoper"
	validatorPubKeyPrefix := AccountAddressPrefix + "valoperpub"
	consNodeAddressPrefix := AccountAddressPrefix + "valcons"
	consNodePubKeyPrefix := AccountAddressPrefix + "valconspub"

	// Set and seal config
	config := sdk.GetConfig()
	config.SetCoinType(ChainCoinType)
	config.SetBech32PrefixForAccount(AccountAddressPrefix, accountPubKeyPrefix)
	config.SetBech32PrefixForValidator(validatorAddressPrefix, validatorPubKeyPrefix)
	config.SetBech32PrefixForConsensusNode(consNodeAddressPrefix, consNodePubKeyPrefix)
	config.Seal()
}
