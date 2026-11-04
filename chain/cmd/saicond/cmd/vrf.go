package cmd

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/saicon-network/saicon/chain/crypto/vrf"
)

const (
	flagOutput     = "output"
	flagVrfKeyFile = "vrf-key-file"
)

// NewVrfCmd returns the `vrf` command: generating and showing the VRF key a
// validator operator offers to the entity registry and signs x/randomness
// submissions with.
func NewVrfCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vrf",
		Short: "Manage the VRF key used for x/randomness submissions",
	}
	cmd.AddCommand(newVrfKeygenCmd(), newVrfShowCmd())
	return cmd
}

func newVrfKeygenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keygen --output <file>",
		Short: "Generate a VRF key and write it to a new file (mode 0600)",
		Long: "Generates an ECVRF-EDWARDS25519-SHA512-ELL2 key from the system's secure random source, " +
			"writes the seed to <file> with mode 0600 and prints the 64-character public key. " +
			"The file is never overwritten; keep it private. Only the operator account can sign MsgSubmitVrf, " +
			"so a leaked key file cannot submit; but whoever holds it can compute the entity's output early.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := cmd.Flags().GetString(flagOutput)
			if err != nil {
				return err
			}
			if path == "" {
				return errors.New("--" + flagOutput + " is required")
			}
			key, err := vrf.GenerateKey(nil)
			if err != nil {
				return err
			}
			if err := vrf.SaveKeyFile(path, key); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "VRF public key: %s\nKey file: %s\n",
				hex.EncodeToString(key.PublicKey()), path)
			return err
		},
	}
	cmd.Flags().String(flagOutput, "", "path of the key file to create (must not exist)")
	return cmd
}

func newVrfShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show --vrf-key-file <file>",
		Short: "Print the public key of a VRF key file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := cmd.Flags().GetString(flagVrfKeyFile)
			if err != nil {
				return err
			}
			if path == "" {
				return errors.New("--" + flagVrfKeyFile + " is required")
			}
			key, err := vrf.LoadKeyFile(path)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), hex.EncodeToString(key.PublicKey()))
			return err
		},
	}
	cmd.Flags().String(flagVrfKeyFile, "", "path of the VRF key file")
	return cmd
}
