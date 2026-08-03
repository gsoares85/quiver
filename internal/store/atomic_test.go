package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")

	require.NoError(t, atomicWriteFile(path, []byte("hello")))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))

	// Overwriting replaces the content in place.
	require.NoError(t, atomicWriteFile(path, []byte("world")))
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "world", string(got))

	// No temporary files are left behind.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "f.txt", entries[0].Name())
}

func TestAtomicWriteFilePerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	require.NoError(t, atomicWriteFile(path, []byte("x")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestAtomicWriteFileMissingDir(t *testing.T) {
	err := atomicWriteFile(filepath.Join(t.TempDir(), "nope", "f.txt"), []byte("x"))
	require.Error(t, err) // parent directory does not exist
}

func TestAtomicWriteFileRenameError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(target, 0o755)) // target is a directory

	err := atomicWriteFile(target, []byte("x")) // renaming a file over a directory fails
	require.Error(t, err)

	// The temp file is cleaned up despite the failure.
	entries, err2 := os.ReadDir(dir)
	require.NoError(t, err2)
	require.Len(t, entries, 1)
	require.Equal(t, "sub", entries[0].Name())
}

func TestEnsureGitignoreCreates(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, ensureGitignore(dir))

	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	require.Contains(t, string(got), ".quiver/")
}

func TestEnsureGitignoreIdempotent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, ensureGitignore(dir))
	require.NoError(t, ensureGitignore(dir)) // second call is a no-op

	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(got), ".quiver/"))
}

func TestEnsureGitignorePreservesExisting(t *testing.T) {
	dir := t.TempDir()
	gi := filepath.Join(dir, ".gitignore")
	require.NoError(t, os.WriteFile(gi, []byte("node_modules"), 0o644)) // no trailing newline

	require.NoError(t, ensureGitignore(dir))

	got, err := os.ReadFile(gi)
	require.NoError(t, err)
	require.Equal(t, "node_modules\n.quiver/\n", string(got))
}
