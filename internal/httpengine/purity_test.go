package httpengine

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPackagePurity asserts that the engine's production sources never reach into
// storage, scripting, the collection runner, or the desktop shell. The engine has to stay
// a leaf that both front-ends can call, because that is what makes a request run
// identically in the app and in CI — the moment it knew about files or the UI, there
// would be two ways to run a request.
func TestPackagePurity(t *testing.T) {
	forbidden := []string{
		"github.com/gsoares85/quiver/internal/store",
		"github.com/gsoares85/quiver/internal/script",
		"github.com/gsoares85/quiver/internal/runner",
		"github.com/gsoares85/quiver/internal/sync",
		"github.com/wailsapp",
		"github.com/dop251/goja",
	}

	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue // production code only
		}
		astFile, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		require.NoError(t, err)

		for _, imp := range astFile.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, prefix := range forbidden {
				require.Falsef(t, strings.HasPrefix(path, prefix), "%s must not import %q", file, path)
			}
		}
	}
}
