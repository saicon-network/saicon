package app

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	participationkeeper "github.com/saicon-network/saicon/chain/x/participation/keeper"
)

// logS10State records, at the v0.11.0 upgrade and after its module migrations,
// what S10 brought into the state: the four WARP params x/participation's
// Migrate1to2 wrote, whether they pass the gate every WARP reader uses, and how
// many WARP rows exist (none: every validator starts from warp_initial at its
// first scored epoch after the upgrade). It writes nothing; a read error is
// logged, never returned — the record is for the operators, not for consensus.
// The PoP route through x/distribution needs no state change and is not logged.
func logS10State(ctx sdk.Context, pk participationkeeper.Keeper) {
	logger := ctx.Logger()
	params, err := pk.Params.Get(ctx)
	if err != nil {
		logger.Error("v0.11.0: participation params not readable", "error", err)
		return
	}
	valid := "valid"
	if err := params.ValidateWarp(); err != nil {
		valid = err.Error()
		logger.Error("v0.11.0: the WARP params do not validate; the voting-power preview will refuse to answer", "error", err)
	}
	rows := 0
	it, err := pk.Warp.Iterate(ctx, nil)
	if err != nil {
		logger.Error("v0.11.0: WARP rows not readable", "error", err)
		return
	}
	for ; it.Valid(); it.Next() {
		rows++
	}
	if err := it.Close(); err != nil {
		logger.Error("v0.11.0: closing the WARP iterator", "error", err)
	}
	logger.Info("v0.11.0: S10 WARP params in force",
		"warp_fall", params.WarpFall.String(), "warp_rise", params.WarpRise.String(),
		"warp_initial", params.WarpInitial.String(), "warp_threshold", params.WarpThreshold.String(),
		"params", valid, "warp_rows", fmt.Sprint(rows),
		"epoch_length_blocks", fmt.Sprint(params.EpochLengthBlocks))
}
