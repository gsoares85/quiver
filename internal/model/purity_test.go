package model

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPackagePurity asserts that the production sources of this package never import
// I/O, network, or scripting dependencies. The model must stay pure data + validation
// so both the desktop shell and the CLI can depend on it freely.
func TestPackagePurity(t *testing.T) {
	forbidden := map[string]bool{
		"net/http":               true,
		"os":                     true,
		"github.com/dop251/goja": true,
	}

	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue // production code only
		}
		astFile, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imp := range astFile.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			require.Falsef(t, forbidden[path], "%s must not import %q", f, path)
		}
	}
}
