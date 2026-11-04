package cli_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/randomness/client/cli"
)

// The help tells clients when to submit: the chain accepts height O, but a
// simulation at latest height O-1 is refused, so clients submit from O+1.
func TestSubmitVrfHelpSaysWhenToSubmit(t *testing.T) {
	long := cli.NewSubmitVrfCmd().Long
	require.Contains(t, long, "from the window's first height O")
	require.Contains(t, long, "O-1 is refused")
	require.Contains(t, long, "submit from O+1")
}
