package cmd

import (
	"bytes"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
)

// runVrf executes the vrf command with args and returns what it printed.
func runVrf(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewVrfCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// keygen writes a 0600 key file and prints its public key; show prints the
// public key of an existing file; keygen never overwrites.
func TestVrfKeygenAndShow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vrf_key.json")

	out, err := runVrf(t, "keygen", "--output", path)
	require.NoError(t, err)

	key, err := vrf.LoadKeyFile(path)
	require.NoError(t, err)
	want := hex.EncodeToString(key.PublicKey())
	require.Len(t, want, 64)
	require.Contains(t, out, want, "keygen prints the public key")

	out, err = runVrf(t, "show", "--vrf-key-file", path)
	require.NoError(t, err)
	require.Equal(t, want+"\n", out, "show prints the 64 hex characters and nothing else")

	_, err = runVrf(t, "keygen", "--output", path)
	require.Error(t, err, "a second keygen to the same path must refuse")
	again, err := vrf.LoadKeyFile(path)
	require.NoError(t, err)
	require.Equal(t, key.Seed(), again.Seed(), "the refused keygen left the key alone")
}

// keygen without --output and show without --vrf-key-file are usage errors.
func TestVrfCommandsRequireTheirFlag(t *testing.T) {
	_, err := runVrf(t, "keygen")
	require.Error(t, err)
	_, err = runVrf(t, "show")
	require.Error(t, err)
}

// The keygen help states what a leaked key file can and cannot do: only the
// operator account signs MsgSubmitVrf.
func TestVrfKeygenHelpStatesTheKeyFilePower(t *testing.T) {
	out, err := runVrf(t, "keygen", "--help")
	require.NoError(t, err)
	require.Contains(t, out, "Only the operator account can sign MsgSubmitVrf")
	require.Contains(t, out, "cannot submit")
	require.NotContains(t, out, "can sign for the validator")
}
