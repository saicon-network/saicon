package keeper_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	"github.com/saicon-network/saicon/chain/x/randomness/keeper"
	"github.com/saicon-network/saicon/chain/x/randomness/types"
)

// TestNothingOutsideReadsTheDraw keeps shadow mode structural (spec §10.3): no
// non-test package outside x/randomness imports it, except app (wiring) and
// cmd/saicond (the CLI). A selection layer that reads the seed must change this
// test, in review.
func TestNothingOutsideReadsTheDraw(t *testing.T) {
	root := filepath.Join("..", "..", "..") // chain/
	allowed := []string{"x/randomness/", "app/", "cmd/saicond/"}
	const pkg = "github.com/saicon-network/saicon/chain/x/randomness"

	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
			strings.HasSuffix(name, ".pb.go") || strings.HasSuffix(name, ".pb.gw.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		checked++
		for _, imp := range file.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if p != pkg && !strings.HasPrefix(p, pkg+"/") {
				continue
			}
			ok := false
			for _, prefix := range allowed {
				if strings.HasPrefix(rel, prefix) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("%s imports %s: only x/randomness, app and cmd/saicond may", rel, p)
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, checked, 100, "the walk found the chain's sources")
}

// TestTheKeeperExportsNoDrawReader: no exported method returns a draw and no
// exported field is the draw history.
func TestTheKeeperExportsNoDrawReader(t *testing.T) {
	isDraw := func(typ reflect.Type) bool {
		for typ.Kind() == reflect.Slice || typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Array {
			typ = typ.Elem()
		}
		return typ == reflect.TypeOf(types.Draw{})
	}
	kt := reflect.TypeOf(&keeper.Keeper{})
	require.Positive(t, kt.NumMethod())
	for i := 0; i < kt.NumMethod(); i++ {
		m := kt.Method(i)
		for j := 0; j < m.Type.NumOut(); j++ {
			require.Falsef(t, isDraw(m.Type.Out(j)), "Keeper.%s returns %s", m.Name, m.Type.Out(j))
		}
	}

	drawMap := reflect.TypeOf(collections.Map[uint64, types.Draw]{})
	st := kt.Elem()
	for i := 0; i < st.NumField(); i++ {
		field := st.Field(i)
		if field.IsExported() {
			require.NotEqualf(t, drawMap, field.Type, "Keeper.%s exposes the draw history", field.Name)
		}
	}
}
