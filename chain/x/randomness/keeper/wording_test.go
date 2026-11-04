package keeper_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNoManipulationClaims: nothing the W2 code ships (generated protobuf
// comments included) says the VRF eliminates, prevents or removes
// manipulation (spec §3). A negated sentence fails too: write "removes
// grinding and leaves a bounded residual".
func TestNoManipulationClaims(t *testing.T) {
	claim := regexp.MustCompile(`(?i)(eliminat|prevent|remov)\w*\s+(\w+\s+){0,3}manipulation`)
	root := filepath.Join("..", "..", "..") // chain/
	dirs := []string{"x/randomness", "crypto/vrf", "x/entityregistry/client"}

	checked := 0
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			checked++
			for n, line := range strings.Split(string(data), "\n") {
				if claim.MatchString(line) {
					t.Errorf("%s:%d: %s", path, n+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		require.NoError(t, err)
	}
	require.Greater(t, checked, 10)
}
