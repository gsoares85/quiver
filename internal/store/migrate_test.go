package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDocVersion(t *testing.T) {
	require.Equal(t, 3, docVersion(map[string]any{"schemaVersion": 3}))
	require.Equal(t, 3, docVersion(map[string]any{"schemaVersion": int64(3)}))
	require.Equal(t, 3, docVersion(map[string]any{"schemaVersion": float64(3)}))
	require.Equal(t, 0, docVersion(map[string]any{}))                        // missing
	require.Equal(t, 0, docVersion(map[string]any{"schemaVersion": "nope"})) // wrong type
}

func TestApplyMigrations(t *testing.T) {
	// Synthetic v0 -> v1: rename "title" to "name" and advance the version.
	migs := []migration{
		func(doc map[string]any) (map[string]any, error) {
			doc["name"] = doc["title"]
			delete(doc, "title")
			doc["schemaVersion"] = 1
			return doc, nil
		},
	}
	got, err := applyMigrations(map[string]any{"schemaVersion": 0, "title": "Hi"}, migs)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"schemaVersion": 1, "name": "Hi"}, got)
}

func TestApplyMigrationsCurrentIsNoOp(t *testing.T) {
	doc := map[string]any{"schemaVersion": currentSchemaVersion, "name": "x"}
	got, err := applyMigrations(doc, nil)
	require.NoError(t, err)
	require.Equal(t, doc, got)
}

func TestApplyMigrationsRejectsFuture(t *testing.T) {
	_, err := applyMigrations(map[string]any{"schemaVersion": currentSchemaVersion + 1}, nil)
	require.Error(t, err)
}

func TestApplyMigrationsMissingStep(t *testing.T) {
	_, err := applyMigrations(map[string]any{"schemaVersion": 0}, nil) // no v0 -> v1 registered
	require.Error(t, err)
}

func TestApplyMigrationsMustAdvance(t *testing.T) {
	migs := []migration{
		func(doc map[string]any) (map[string]any, error) { return doc, nil }, // leaves version at 0
	}
	_, err := applyMigrations(map[string]any{"schemaVersion": 0}, migs)
	require.Error(t, err)
}

func TestLoadRejectsFutureSchemaVersion(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, manifestFile),
		[]byte("schemaVersion: 999\nid: ws\nname: n\n"), 0o644))

	_, err := Load(context.Background(), root)
	require.Error(t, err)
}

func TestLoadMigratesOlderFile(t *testing.T) {
	// Temporarily register a synthetic v0 -> v1 migration.
	saved := migrations
	migrations = []migration{
		func(doc map[string]any) (map[string]any, error) {
			doc["schemaVersion"] = 1
			return doc, nil
		},
	}
	t.Cleanup(func() { migrations = saved })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, manifestFile),
		[]byte("schemaVersion: 0\nid: ws\nname: Legacy\n"), 0o644))

	ws, err := Load(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, "Legacy", ws.Name)
	require.Equal(t, currentSchemaVersion, ws.SchemaVersion)
}
