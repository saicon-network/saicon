package chain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/indexer/internal/chain"
)

// The two forms of one validator, taken from the running localnet on 2026-09-22.
const (
	opAddr  = "saiconvaloper1yh0ceztycl23zyuy67776u5dtfjawnrfytvjsd"
	accAddr = "saicon1yh0ceztycl23zyuy67776u5dtfjawnrfuvlyfs"
	rawHex  = "25df8c8964c7d5111384d7bded728d5a65d74c69"

	consPubKeyB64 = "FCgG3DkJbOI0/ONJfmRFHtBBh/YBBZgWe+slKKLZWF8="
	consAddrHex   = "245BF4B5FA75F0B7E76D153A114571FC8A3DEFBA"
)

func TestAddrKeyCollapsesTheTwoPrefixes(t *testing.T) {
	a, err := chain.AddrKey(opAddr)
	require.NoError(t, err)
	b, err := chain.AddrKey(accAddr)
	require.NoError(t, err)

	require.Equal(t, rawHex, a)
	require.Equal(t, rawHex, b)
	require.NotEqual(t, opAddr, accAddr,
		"the strings differ — which is exactly why the join cannot use them")
}

func TestAddrKeyRejectsRubbish(t *testing.T) {
	for _, bad := range []string{"", "1", "nodelimiter", "saicon1short"} {
		_, err := chain.AddrKey(bad)
		require.Error(t, err, "input %q must not decode", bad)
	}
}

func TestConsAddrFromPubKeyMatchesTheBlockHeader(t *testing.T) {
	got, err := chain.ConsAddrFromPubKey(consPubKeyB64)
	require.NoError(t, err)
	require.Equal(t, consAddrHex, got,
		"this is the string CometBFT puts in proposer_address; if it drifts the proposal count silently goes to zero")
}
