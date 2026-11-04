package vrf

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyFileRoundTrip(t *testing.T) {
	k, err := GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "vrf_key.json")
	if err := SaveKeyFile(path, k); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]string
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["suite"] != "ECVRF-EDWARDS25519-SHA512-ELL2" {
		t.Fatalf("suite %q", doc["suite"])
	}
	loaded, err := LoadKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.PublicKey(), k.PublicKey()) {
		t.Fatal("the loaded key differs")
	}
}

func TestKeyFileRefusesOverwriteAndTampering(t *testing.T) {
	k, err := GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "vrf_key.json")
	if err := SaveKeyFile(path, k); err != nil {
		t.Fatal(err)
	}
	if err := SaveKeyFile(path, other); err == nil {
		t.Fatal("a second SaveKeyFile overwrote the file")
	}
	loaded, err := LoadKeyFile(path)
	if err != nil || !bytes.Equal(loaded.PublicKey(), k.PublicKey()) {
		t.Fatalf("the original file did not survive: %v", err)
	}

	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	seed := func(key PrivateKey) string { return hex.EncodeToString(key.Seed()) }
	pub := func(key PrivateKey) string { return hex.EncodeToString(key.PublicKey()) }
	doc := func(suite, pk, sd string) string {
		return `{"suite":"` + suite + `","public_key":"` + pk + `","seed":"` + sd + `"}`
	}

	bad := map[string]string{
		"mismatched public key": doc(Suite, pub(other), seed(k)),
		"other suite":           doc("ECVRF-EDWARDS25519-SHA512-TAI", pub(k), seed(k)),
		"missing suite":         `{"public_key":"` + pub(k) + `","seed":"` + seed(k) + `"}`,
		"short seed":            doc(Suite, pub(k), seed(k)[:62]),
		"non-hex seed":          doc(Suite, pub(k), strings.Repeat("zz", SeedSize)),
		"not json":              "seed",
	}
	for name, body := range bad {
		if _, err := LoadKeyFile(write(strings.ReplaceAll(name, " ", "_")+".json", body)); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
	if _, err := LoadKeyFile(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("a missing file loaded")
	}
}
