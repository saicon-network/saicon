package keeper_test

import (
	"cosmossdk.io/math"

	"github.com/saicon-network/saicon/chain/x/rewards/types"
)

// registerV1Params is DefaultParams with the Register v1.0 security-budget rates
// (0.15 / 0.005 / 0.0075). The emission tests check MECHANICS — clipping, the
// carry, the gas offset, the epoch gate — against figures derived from those
// rates. Pinning them here keeps those tests meaningful when the defaults move,
// as they did in S6 (÷7). The defaults themselves are pinned separately, in
// TestParams_DefaultsAreTheRegisterRatesDividedBySeven.
func registerV1Params() types.Params {
	p := types.DefaultParams()
	p.BudgetBondedRate = math.LegacyNewDecWithPrec(15, 2)
	p.BudgetFloorRate = math.LegacyNewDecWithPrec(5, 3)
	p.BudgetCeilingRate = math.LegacyNewDecWithPrec(75, 4)
	return p
}
