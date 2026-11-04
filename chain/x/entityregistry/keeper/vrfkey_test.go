package keeper_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

const testChainID = "saicon-test"

// vrfKey is the key built from a seed of 32 copies of b.
func vrfKey(t *testing.T, b byte) vrf.PrivateKey {
	t.Helper()
	k, err := vrf.NewKeyFromSeed(bytes.Repeat([]byte{b}, 32))
	require.NoError(t, err)
	return k
}

// offerFor returns the hex key and the hex proof of possession a validator's
// operator sends in MsgOfferVrfKey.
func offerFor(t *testing.T, k vrf.PrivateKey, v sdk.ValAddress, chainID string) (pkHex, popHex string) {
	t.Helper()
	return hex.EncodeToString(k.PublicKey()),
		hex.EncodeToString(k.Prove(types.PopAlpha(chainID, v, k.PublicKey())))
}

// offer makes the operator of v offer key k, with a valid proof.
func (f *fixture) offer(t *testing.T, k vrf.PrivateKey, v sdk.ValAddress) {
	t.Helper()
	pk, pop := offerFor(t, k, v, testChainID)
	require.NoError(t, f.k.OfferVrfKey(f.ctx, sdk.AccAddress(v), pk, pop))
}

func TestOfferStoresTheOfferAndTheOwner(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	k := vrfKey(t, 1)
	pk, pop := offerFor(t, k, val(1), testChainID)

	// A call that fails at step 1 is the gas baseline.
	before := f.ctx.GasMeter().GasConsumed()
	require.ErrorIs(t, f.k.OfferVrfKey(f.ctx, acc(1), "zz", pop), types.ErrInvalidVrfKey)
	failed := f.ctx.GasMeter().GasConsumed() - before

	before = f.ctx.GasMeter().GasConsumed()
	require.NoError(t, f.k.OfferVrfKey(f.ctx, acc(1), pk, pop))
	spent := f.ctx.GasMeter().GasConsumed() - before
	require.GreaterOrEqual(t, spent, failed+vrf.VerifyGas)

	got, err := f.k.VrfKeyOffers.Get(f.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, types.VrfKeyOffer{Validator: val(1).String(), VrfPublicKey: pk, Height: 10}, got)
	owner, err := f.k.VrfKeyOwners.Get(f.ctx, k.PublicKey())
	require.NoError(t, err)
	require.Equal(t, []byte(val(1)), owner)
	requireEvent(t, f.ctx, types.EventTypeVrfKeyOffered, "vrf_public_key", pk)
	requireEvent(t, f.ctx, types.EventTypeVrfKeyOffered, "validator", val(1).String())
}

func TestOfferRefuses(t *testing.T) {
	k1, k2 := vrfKey(t, 1), vrfKey(t, 2)
	pk1, pop1 := offerFor(t, k1, val(1), testChainID)
	_, popOtherValidator := offerFor(t, k1, val(2), testChainID)
	_, popOtherChain := offerFor(t, k1, val(1), "other-chain")
	popOtherKey := hex.EncodeToString(k2.Prove(types.PopAlpha(testChainID, val(1), k1.PublicKey())))

	cases := []struct {
		name    string
		pk, pop string
		prep    func(f *fixture)
		want    error
	}{
		{"bad key hex", "zz", pop1, nil, types.ErrInvalidVrfKey},
		{"short key", pk1[:62], pop1, nil, types.ErrInvalidVrfKey},
		{"bad pop hex", pk1, "zz", nil, types.ErrInvalidProofOfPossession},
		{"pop made for another validator", pk1, popOtherValidator, nil, types.ErrInvalidProofOfPossession},
		{"pop made for another chain", pk1, popOtherChain, nil, types.ErrInvalidProofOfPossession},
		{"pop made by another key", pk1, popOtherKey, nil, types.ErrInvalidProofOfPossession},
		{"key owned by another validator", pk1, pop1, func(f *fixture) {
			require.NoError(t, f.k.VrfKeyOwners.Set(f.ctx, k1.PublicKey(), val(2)))
		}, types.ErrVrfKeyInUse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.ctx = f.ctx.WithChainID(testChainID)
			if tc.prep != nil {
				tc.prep(f)
			}
			owners := countOwners(t, f)
			require.ErrorIs(t, f.k.OfferVrfKey(f.ctx, acc(1), tc.pk, tc.pop), tc.want)
			ok, err := f.k.VrfKeyOffers.Has(f.ctx, val(1))
			require.NoError(t, err)
			require.False(t, ok, "a refused offer writes no offer")
			require.Equal(t, owners, countOwners(t, f), "a refused offer writes no owner")
		})
	}
}

func countOwners(t *testing.T, f *fixture) int {
	t.Helper()
	n := 0
	require.NoError(t, f.k.VrfKeyOwners.Walk(f.ctx, nil, func(_, _ []byte) (bool, error) {
		n++
		return false, nil
	}))
	return n
}

func TestOfferReplacesOwnOffer(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	k1, k2 := vrfKey(t, 1), vrfKey(t, 2)
	f.offer(t, k1, val(1))
	f.ctx = f.ctx.WithBlockHeight(20)
	f.offer(t, k2, val(1))

	got, err := f.k.VrfKeyOffers.Get(f.ctx, val(1))
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(k2.PublicKey()), got.VrfPublicKey)
	require.Equal(t, int64(20), got.Height)
	// K13: the earlier key stays owned, never pruned.
	for _, k := range []vrf.PrivateKey{k1, k2} {
		owner, err := f.k.VrfKeyOwners.Get(f.ctx, k.PublicKey())
		require.NoError(t, err)
		require.Equal(t, []byte(val(1)), owner)
	}

	pk, pop := offerFor(t, k1, val(2), testChainID)
	require.ErrorIs(t, f.k.OfferVrfKey(f.ctx, acc(2), pk, pop), types.ErrVrfKeyInUse)
}

func TestOfferingTheSameKeyAgainIsAccepted(t *testing.T) {
	f := newFixture(t)
	f.ctx = f.ctx.WithChainID(testChainID)
	k := vrfKey(t, 1)
	f.offer(t, k, val(1))
	f.offer(t, k, val(1))
}
