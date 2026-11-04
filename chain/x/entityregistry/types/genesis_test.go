package types_test

import (
	"bytes"
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/entityregistry/types"
)

func valoperStr(b byte) string {
	return sdk.MustBech32ifyAddressBytes(types.ValidatorPrefix, bytes.Repeat([]byte{b}, 20))
}

func attested(id string) types.Entity {
	return types.Entity{
		Id:              id,
		DisplayName:     "Entity " + id,
		AttestationHash: strings.Repeat("ab", 32),
		Attestor:        sdk.MustBech32ifyAddressBytes(types.AccountPrefix, bytes.Repeat([]byte{9}, 20)),
		Status:          types.EntityStatus_ENTITY_STATUS_ATTESTED,
		AttestedHeight:  5,
	}
}

func revoked(id string) types.Entity {
	e := attested(id)
	e.Status = types.EntityStatus_ENTITY_STATUS_REVOKED
	e.RevokedHeight = 9
	e.RevokeReason = "withdrawn"
	return e
}

func member(b byte, entity string) types.Member {
	return types.Member{Validator: valoperStr(b), EntityId: entity, AttestedHeight: 5}
}

// goodGenesis is a valid state with two attested entities, one revoked entity,
// a frozen seat and a pending validator.
func goodGenesis() *types.GenesisState {
	return &types.GenesisState{
		Params:   types.DefaultParams(),
		Entities: []types.Entity{attested("alpha"), attested("beta"), revoked("gamma")},
		Members:  []types.Member{member(1, "alpha"), member(2, "alpha"), member(3, "beta")},
		Frozen:   []types.FrozenSeat{{Validator: valoperStr(4), FormerEntityId: "gamma", Reason: "revoked", Height: 9}},
		Pending:  []string{valoperStr(5)},
	}
}

func TestDefaultGenesisIsValid(t *testing.T) {
	require.NoError(t, types.DefaultGenesis().Validate())
	require.NoError(t, goodGenesis().Validate())
}

func TestGenesisRejects(t *testing.T) {
	nine := func() []types.Member {
		ms := make([]types.Member, 9)
		for i := range ms {
			ms[i] = member(byte(i+20), "alpha")
		}
		return ms
	}
	for _, tc := range []struct {
		name string
		mut  func(*types.GenesisState)
	}{
		{"bad params", func(g *types.GenesisState) { g.Params.MaxActiveSeats = 0 }},
		{"duplicate entity id", func(g *types.GenesisState) { g.Entities = append(g.Entities, attested("alpha")) }},
		{"malformed entity id", func(g *types.GenesisState) { g.Entities[0].Id = "Alpha" }},
		{"malformed name", func(g *types.GenesisState) { g.Entities[0].DisplayName = "" }},
		{"malformed hash", func(g *types.GenesisState) { g.Entities[0].AttestationHash = "abc" }},
		{"unspecified status", func(g *types.GenesisState) { g.Entities[0].Status = types.EntityStatus_ENTITY_STATUS_UNSPECIFIED }},
		{"attested with 0 members", func(g *types.GenesisState) {
			g.Entities = append(g.Entities, attested("delta"))
		}},
		{"attested with 9 members", func(g *types.GenesisState) {
			g.Members = append(nine(), member(3, "beta"))
		}},
		{"revoked with a member", func(g *types.GenesisState) {
			g.Members = append(g.Members, member(6, "gamma"))
		}},
		{"member of unknown entity", func(g *types.GenesisState) {
			g.Members = append(g.Members, member(6, "nowhere"))
		}},
		{"same validator twice", func(g *types.GenesisState) {
			g.Members = append(g.Members, member(1, "beta"))
		}},
		{"member with bad address", func(g *types.GenesisState) {
			g.Members[0].Validator = sdk.MustBech32ifyAddressBytes(types.AccountPrefix, bytes.Repeat([]byte{1}, 20))
		}},
		{"frozen validator also a member", func(g *types.GenesisState) {
			g.Frozen[0].Validator = valoperStr(1)
		}},
		{"frozen twice", func(g *types.GenesisState) {
			g.Frozen = append(g.Frozen, g.Frozen[0])
		}},
		{"frozen with bad address", func(g *types.GenesisState) {
			g.Frozen[0].Validator = "bad"
		}},
		{"pending with wrong prefix", func(g *types.GenesisState) {
			g.Pending = []string{sdk.MustBech32ifyAddressBytes(types.AccountPrefix, bytes.Repeat([]byte{5}, 20))}
		}},
		{"pending twice, uppercase spelling", func(g *types.GenesisState) {
			g.Pending = []string{valoperStr(5), strings.ToUpper(valoperStr(5))}
		}},
		{"member twice, uppercase spelling", func(g *types.GenesisState) {
			g.Members = append(g.Members, types.Member{Validator: strings.ToUpper(valoperStr(1)), EntityId: "beta", AttestedHeight: 5})
		}},
		{"frozen also a member, uppercase spelling", func(g *types.GenesisState) {
			g.Frozen[0].Validator = strings.ToUpper(valoperStr(1))
		}},
		{"frozen twice, uppercase spelling", func(g *types.GenesisState) {
			dup := g.Frozen[0]
			dup.Validator = strings.ToUpper(dup.Validator)
			g.Frozen = append(g.Frozen, dup)
		}},
		{"entity attestor not an account address", func(g *types.GenesisState) {
			g.Entities[0].Attestor = valoperStr(9)
		}},
		{"entity attestor empty", func(g *types.GenesisState) { g.Entities[0].Attestor = "" }},
		{"frozen former_entity_id malformed", func(g *types.GenesisState) {
			g.Frozen[0].FormerEntityId = "Bad_ID"
		}},
		{"pending twice", func(g *types.GenesisState) {
			g.Pending = []string{valoperStr(5), valoperStr(5)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := goodGenesis()
			tc.mut(g)
			require.Error(t, g.Validate())
		})
	}
}

// departure builds a valid departure of validator b from entity "gamma".
func departure(b byte) types.Departure {
	return types.Departure{Validator: valoperStr(b), EntityId: "gamma", JoinedHeight: 3, LeftHeight: 9}
}

func TestGenesisDepartures(t *testing.T) {
	g := goodGenesis()
	g.Members[0].JoinedHeight = 5
	frozen := departure(7)
	frozen.Frozen, frozen.ChargedUntil = true, 12
	stillCharged := departure(8)
	stillCharged.Frozen = true
	g.Departures = []types.Departure{departure(6), frozen, stillCharged}
	require.NoError(t, g.Validate())

	// A departure that left the moment it joined, and one charged to its left height, are legal.
	edge := departure(9)
	edge.JoinedHeight, edge.LeftHeight, edge.Frozen, edge.ChargedUntil = 9, 9, true, 9
	g.Departures = append(g.Departures, edge)
	require.NoError(t, g.Validate())

	d := types.DefaultGenesis()
	require.NotNil(t, d.Departures)
	require.Empty(t, d.Departures)

	for _, tc := range []struct {
		name string
		mut  func(*types.GenesisState)
	}{
		{"two departures for one validator", func(g *types.GenesisState) {
			g.Departures = []types.Departure{departure(6), departure(6)}
		}},
		{"two departures, uppercase spelling", func(g *types.GenesisState) {
			dup := departure(6)
			dup.Validator = strings.ToUpper(dup.Validator)
			g.Departures = []types.Departure{departure(6), dup}
		}},
		{"bad entity id", func(g *types.GenesisState) {
			bad := departure(6)
			bad.EntityId = "Bad_ID"
			g.Departures = []types.Departure{bad}
		}},
		{"left before joined", func(g *types.GenesisState) {
			bad := departure(6)
			bad.LeftHeight = bad.JoinedHeight - 1
			g.Departures = []types.Departure{bad}
		}},
		{"negative joined height", func(g *types.GenesisState) {
			bad := departure(6)
			bad.JoinedHeight = -1
			g.Departures = []types.Departure{bad}
		}},
		{"charged_until below left_height", func(g *types.GenesisState) {
			bad := departure(6)
			bad.Frozen, bad.ChargedUntil = true, bad.LeftHeight-1
			g.Departures = []types.Departure{bad}
		}},
		{"negative charged_until", func(g *types.GenesisState) {
			bad := departure(6)
			bad.Frozen, bad.ChargedUntil = true, -1
			g.Departures = []types.Departure{bad}
		}},
		{"charged_until without a frozen seat", func(g *types.GenesisState) {
			bad := departure(6)
			bad.ChargedUntil = bad.LeftHeight + 1
			g.Departures = []types.Departure{bad}
		}},
		{"member with negative joined_height", func(g *types.GenesisState) {
			g.Members[0].JoinedHeight = -1
		}},
		{"departure validator is a cosmosvaloper", func(g *types.GenesisState) {
			bad := departure(6)
			bad.Validator = sdk.MustBech32ifyAddressBytes("cosmosvaloper", bytes.Repeat([]byte{6}, 20))
			g.Departures = []types.Departure{bad}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := goodGenesis()
			tc.mut(g)
			require.Error(t, g.Validate())
		})
	}
}

// withKeys returns goodGenesis with validators 1 and 2 bound to RFC keys 19 and
// 20, validator 3 holding a standing offer for key 21, and the owner entries.
func withKeys() *types.GenesisState {
	g := goodGenesis()
	g.Members[0].VrfPublicKey, g.Members[0].VrfKeyHeight = rfcKey19, 7
	g.Members[1].VrfPublicKey, g.Members[1].VrfKeyHeight = rfcKey20, 8
	g.VrfKeyOffers = []types.VrfKeyOffer{{Validator: valoperStr(3), VrfPublicKey: rfcKey21, Height: 6}}
	g.VrfKeyOwners = []types.VrfKeyOwner{
		{VrfPublicKey: rfcKey19, Validator: valoperStr(1)},
		{VrfPublicKey: rfcKey20, Validator: valoperStr(2)},
		{VrfPublicKey: rfcKey21, Validator: valoperStr(3)},
	}
	return g
}

func TestGenesisVrfKeys(t *testing.T) {
	require.NoError(t, withKeys().Validate())

	d := types.DefaultGenesis()
	require.NoError(t, d.Validate())
	require.NotNil(t, d.VrfKeyOffers)
	require.Empty(t, d.VrfKeyOffers)
	require.NotNil(t, d.VrfKeyOwners)
	require.Empty(t, d.VrfKeyOwners)

	for _, tc := range []struct {
		name string
		mut  func(*types.GenesisState)
	}{
		{"member key without an owner entry", func(g *types.GenesisState) {
			g.VrfKeyOwners = g.VrfKeyOwners[1:]
		}},
		{"owner names another validator", func(g *types.GenesisState) {
			g.VrfKeyOwners[0].Validator = valoperStr(2)
		}},
		{"two owner entries for one key", func(g *types.GenesisState) {
			g.VrfKeyOwners = append(g.VrfKeyOwners, types.VrfKeyOwner{VrfPublicKey: rfcKey19, Validator: valoperStr(1)})
		}},
		{"offer without an owner entry", func(g *types.GenesisState) {
			g.VrfKeyOwners = g.VrfKeyOwners[:2]
		}},
		{"offer owned by another validator", func(g *types.GenesisState) {
			g.VrfKeyOffers[0].Validator = valoperStr(1)
		}},
		{"two offers for one validator", func(g *types.GenesisState) {
			g.VrfKeyOffers = append(g.VrfKeyOffers, g.VrfKeyOffers[0])
		}},
		{"negative key height", func(g *types.GenesisState) { g.Members[0].VrfKeyHeight = -1 }},
		{"height without a key", func(g *types.GenesisState) {
			g.Members[2].VrfKeyHeight = 4
		}},
		{"member key uppercase", func(g *types.GenesisState) {
			g.Members[0].VrfPublicKey = strings.ToUpper(rfcKey19)
		}},
		{"member key is the identity point", func(g *types.GenesisState) {
			g.Members[0].VrfPublicKey = identityKeyHex
			g.VrfKeyOwners[0].VrfPublicKey = identityKeyHex
		}},
		{"owner key malformed", func(g *types.GenesisState) {
			g.VrfKeyOwners[2].VrfPublicKey = "abc"
		}},
		{"offer key malformed", func(g *types.GenesisState) {
			g.VrfKeyOffers[0].VrfPublicKey = "abc"
		}},
		{"owner validator wrong prefix", func(g *types.GenesisState) {
			g.VrfKeyOwners[0].Validator = sdk.MustBech32ifyAddressBytes(types.AccountPrefix, bytes.Repeat([]byte{1}, 20))
		}},
		{"two members share a key", func(g *types.GenesisState) {
			g.Members[1].VrfPublicKey = rfcKey19
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := withKeys()
			tc.mut(g)
			require.Error(t, g.Validate())
		})
	}
}
