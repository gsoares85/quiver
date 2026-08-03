package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gsoares85/quiver/internal/model"
	"github.com/stretchr/testify/require"
)

// writeYAML marshals v and writes it to path, creating parent directories.
func writeYAML(t *testing.T, path string, v any) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	data, err := marshalYAML(v)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

// writeFixture lays out a small but complete workspace tree under root.
func writeFixture(t *testing.T, root string) {
	writeYAML(t, filepath.Join(root, manifestFile), fileManifest{
		SchemaVersion: currentSchemaVersion,
		ID:            "ws",
		Name:          "My API",
		Settings:      model.Settings{FollowRedirects: true, TimeoutMs: 30000},
		Order:         []string{"users-api"},
	})
	writeYAML(t, filepath.Join(root, environmentsDir, "local.qv.yaml"), newFileEnvironment(model.Environment{
		ID:        "env-local",
		Name:      "local",
		Variables: []model.Variable{{Key: "baseUrl", Value: "http://localhost", Enabled: true}},
	}))

	col := filepath.Join(root, collectionsDir, "users-api")
	// order lists the folder "users" before the request "ping.qv.yaml".
	writeYAML(t, filepath.Join(col, collectionMeta), fileCollection{
		SchemaVersion: currentSchemaVersion,
		ID:            "col",
		Name:          "Users API",
		Order:         []string{"users", "ping.qv.yaml"},
	})
	writeYAML(t, filepath.Join(col, "users", folderMeta), fileFolder{
		SchemaVersion: currentSchemaVersion,
		ID:            "fld",
		Name:          "Users",
		Order:         []string{"get-user.qv.yaml"},
	})
	writeYAML(t, filepath.Join(col, "users", "get-user.qv.yaml"),
		newFileRequest(model.Request{ID: "req-get", Name: "Get user", Method: "GET", URL: "u"}))
	writeYAML(t, filepath.Join(col, "ping.qv.yaml"),
		newFileRequest(model.Request{ID: "req-ping", Name: "Ping", Method: "GET", URL: "p"}))
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root)

	ws, err := Load(context.Background(), root)
	require.NoError(t, err)

	require.Equal(t, "ws", ws.ID)
	require.Equal(t, "My API", ws.Name)
	require.Equal(t, model.Settings{FollowRedirects: true, TimeoutMs: 30000}, ws.Settings)

	require.Len(t, ws.Environments, 1)
	require.Equal(t, "local", ws.Environments[0].Name)

	require.Len(t, ws.Collections, 1)
	col := ws.Collections[0]
	require.Equal(t, "Users API", col.Name)

	// order: the "users" folder comes before the "ping" request.
	require.Len(t, col.Items, 2)
	require.NotNil(t, col.Items[0].Folder)
	require.Equal(t, "Users", col.Items[0].Folder.Name)
	require.Len(t, col.Items[0].Folder.Items, 1)
	require.Equal(t, "Get user", col.Items[0].Folder.Items[0].Request.Name)
	require.NotNil(t, col.Items[1].Request)
	require.Equal(t, "Ping", col.Items[1].Request.Name)

	require.NoError(t, ws.Validate())
}

func TestLoadAlphabeticalFallback(t *testing.T) {
	root := t.TempDir()
	writeYAML(t, filepath.Join(root, manifestFile), fileManifest{
		SchemaVersion: currentSchemaVersion, ID: "ws", Name: "n",
	})
	col := filepath.Join(root, collectionsDir, "c")
	writeYAML(t, filepath.Join(col, collectionMeta), fileCollection{
		SchemaVersion: currentSchemaVersion, ID: "col", Name: "c", // no Order → alphabetical
	})
	writeYAML(t, filepath.Join(col, "b.qv.yaml"), newFileRequest(model.Request{ID: "b", Name: "B", Method: "GET"}))
	writeYAML(t, filepath.Join(col, "a.qv.yaml"), newFileRequest(model.Request{ID: "a", Name: "A", Method: "GET"}))

	ws, err := Load(context.Background(), root)
	require.NoError(t, err)
	items := ws.Collections[0].Items
	require.Len(t, items, 2)
	require.Equal(t, "A", items[0].Request.Name) // a.qv.yaml sorts before b.qv.yaml
	require.Equal(t, "B", items[1].Request.Name)
}

func TestLoadMissingManifest(t *testing.T) {
	_, err := Load(context.Background(), t.TempDir())
	require.Error(t, err)
}

func TestLoadInvalidYAML(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, manifestFile), []byte("\tbad: [yaml"), 0o644))
	_, err := Load(context.Background(), root)
	require.Error(t, err)
}

func TestLoadCancelledContext(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Load(ctx, root)
	require.Error(t, err)
}

func TestOrderNames(t *testing.T) {
	// "b" is ordered first; "a" and "c" (not listed) follow alphabetically.
	got := orderNames([]string{"a", "b", "c"}, []string{"b", "missing"})
	require.Equal(t, []string{"b", "a", "c"}, got)
}
