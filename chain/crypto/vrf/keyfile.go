package vrf

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// keyFile is the on-disk form of a VRF key: lowercase hex, suite-tagged.
type keyFile struct {
	Suite     string `json:"suite"`
	PublicKey string `json:"public_key"`
	Seed      string `json:"seed"`
}

// SaveKeyFile writes k to a new file at path with mode 0600. It refuses to
// overwrite an existing file.
func SaveKeyFile(path string, k PrivateKey) error {
	body, err := json.Marshal(keyFile{
		Suite:     Suite,
		PublicKey: hex.EncodeToString(k.PublicKey()),
		Seed:      hex.EncodeToString(k.Seed()),
	})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path is operator-chosen
	if err != nil {
		return fmt.Errorf("vrf: creating key file: %w", err)
	}
	// A partial file would make O_EXCL refuse every retry, so remove it.
	fail := func(what string, err error) error {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("vrf: %s key file: %w", what, err)
	}
	if _, err := f.Write(append(body, '\n')); err != nil {
		return fail("writing", err)
	}
	if err := f.Sync(); err != nil {
		return fail("syncing", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("vrf: closing key file: %w", err)
	}
	return nil
}

// LoadKeyFile reads a key written by SaveKeyFile. It checks the suite and
// that the seed derives the stored public key.
func LoadKeyFile(path string) (PrivateKey, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is operator-chosen
	if err != nil {
		return PrivateKey{}, fmt.Errorf("vrf: reading key file: %w", err)
	}
	var doc keyFile
	if err := json.Unmarshal(raw, &doc); err != nil {
		return PrivateKey{}, fmt.Errorf("vrf: parsing key file: %w", err)
	}
	if doc.Suite != Suite {
		return PrivateKey{}, fmt.Errorf("vrf: key file suite %q, want %q", doc.Suite, Suite)
	}
	seed, err := hex.DecodeString(doc.Seed)
	if err != nil {
		return PrivateKey{}, fmt.Errorf("vrf: key file seed: %w", err)
	}
	pk, err := hex.DecodeString(doc.PublicKey)
	if err != nil {
		return PrivateKey{}, fmt.Errorf("vrf: key file public key: %w", err)
	}
	k, err := NewKeyFromSeed(seed)
	if err != nil {
		return PrivateKey{}, err
	}
	if !bytes.Equal(k.PublicKey(), pk) {
		return PrivateKey{}, errors.New("vrf: key file public key does not match its seed")
	}
	return k, nil
}
