package store

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/gsoares85/quiver/internal/model"
	"github.com/stretchr/testify/require"
)

// readTree returns a map of forward-slash relative path -> file content for every file
// under root, so two trees can be compared for byte-for-byte equality.
func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	}))
	return out
}

func sampleWorkspace() *model.Workspace {
	return &model.Workspace{
		SchemaVersion: currentSchemaVersion,
		ID:            "ws-id",
		Name:          "My API",
		Settings:      model.Settings{FollowRedirects: true, TimeoutMs: 30000},
		Environments: []model.Environment{{
			ID:        "env-local",
			Name:      "local",
			Variables: []model.Variable{{Key: "baseUrl", Value: "http://localhost", Enabled: true}},
		}},
		Collections: []model.Collection{{
			ID:   "col",
			Name: "Users API",
			Items: []model.Item{
				{Folder: &model.Folder{ID: "fld", Name: "Users", Items: []model.Item{
					{Request: &model.Request{
						ID: "r1", Name: "Get user", Method: "GET", URL: "u",
						Settings: model.RequestSettings{FollowRedirects: true, MaxRedirects: 10},
					}},
				}}},
				{Request: &model.Request{ID: "r2", Name: "Ping", Method: "GET", URL: "p"}},
			},
		}},
	}
}

func TestSaveRoundTripByteIdentical(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".quiver/\n"), 0o644))

	ws, err := Load(context.Background(), root)
	require.NoError(t, err)

	dest := t.TempDir()
	require.NoError(t, Save(context.Background(), ws, dest))

	require.Equal(t, readTree(t, root), readTree(t, dest))
}

func TestSaveLoadSaveStable(t *testing.T) {
	ctx := context.Background()
	ws := sampleWorkspace()

	d1 := t.TempDir()
	require.NoError(t, Save(ctx, ws, d1))

	loaded, err := Load(ctx, d1)
	require.NoError(t, err)
	require.NoError(t, loaded.Validate())

	d2 := t.TempDir()
	require.NoError(t, Save(ctx, loaded, d2))

	require.Equal(t, readTree(t, d1), readTree(t, d2))
}

func TestSavePrunesRemovedItems(t *testing.T) {
	ctx := context.Background()
	ws := sampleWorkspace()
	dir := t.TempDir()
	require.NoError(t, Save(ctx, ws, dir))

	folderDir := filepath.Join(dir, collectionsDir, "users-api", "users")
	require.DirExists(t, folderDir)

	// Drop the "Users" folder, keeping only the "Ping" request.
	ws.Collections[0].Items = ws.Collections[0].Items[1:]
	require.NoError(t, Save(ctx, ws, dir))

	require.NoDirExists(t, folderDir)
	require.FileExists(t, filepath.Join(dir, collectionsDir, "users-api", "ping.qv.yaml"))
}

// TestSavePrunesRemovedEntities covers the rest of the "dropped from the model" contract
// that TestSavePrunesRemovedItems leaves out: whole collections and environments.
func TestSavePrunesRemovedEntities(t *testing.T) {
	ctx := context.Background()
	ws := sampleWorkspace()
	ws.Collections = append(ws.Collections, model.Collection{ID: "col2", Name: "Orders API"})
	dir := t.TempDir()
	require.NoError(t, Save(ctx, ws, dir))

	ordersDir := filepath.Join(dir, collectionsDir, "orders-api")
	localEnv := filepath.Join(dir, environmentsDir, "local.qv.yaml")
	require.DirExists(t, ordersDir)
	require.FileExists(t, localEnv)

	// Drop the second collection and every environment.
	ws.Collections = ws.Collections[:1]
	ws.Environments = nil
	require.NoError(t, Save(ctx, ws, dir))

	require.NoDirExists(t, ordersDir)
	require.NoFileExists(t, localEnv)
	require.DirExists(t, filepath.Join(dir, collectionsDir, "users-api"))
}

// TestSavePrunesUnrecognizedEntries pins the documented contract: inside the managed
// directories Save removes every entry it did not write, including files and directories
// that never came from the model.
func TestSavePrunesUnrecognizedEntries(t *testing.T) {
	ctx := context.Background()
	ws := sampleWorkspace()
	dir := t.TempDir()
	require.NoError(t, Save(ctx, ws, dir))

	colDir := filepath.Join(dir, collectionsDir, "users-api")
	folderDir := filepath.Join(colDir, "users")

	// One stray entry per managed directory, mixing files and directories.
	strays := []string{
		filepath.Join(dir, collectionsDir, "README.md"),
		filepath.Join(dir, collectionsDir, ".gitkeep"),
		filepath.Join(dir, environmentsDir, "notes.txt"),
		filepath.Join(dir, environmentsDir, "scratch.yaml"),
		filepath.Join(colDir, "NOTES.md"),
		filepath.Join(folderDir, "draft.json"),
	}
	strayDirs := []string{
		filepath.Join(dir, collectionsDir, "scratch"), // a dir without collection.qv.yaml
		filepath.Join(colDir, "attachments"),
		filepath.Join(folderDir, "fixtures"),
	}
	for _, p := range strays {
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	}
	for _, p := range strayDirs {
		require.NoError(t, os.MkdirAll(p, 0o755))
		// Nested content proves the removal is recursive.
		require.NoError(t, os.WriteFile(filepath.Join(p, "nested.txt"), []byte("x"), 0o644))
	}

	require.NoError(t, Save(ctx, ws, dir))

	for _, p := range strays {
		require.NoFileExistsf(t, p, "stray file %s must be pruned", p)
	}
	for _, p := range strayDirs {
		require.NoDirExistsf(t, p, "stray dir %s must be pruned", p)
	}
	// The model's own entries are untouched.
	require.FileExists(t, filepath.Join(colDir, collectionMeta))
	require.FileExists(t, filepath.Join(colDir, "ping.qv.yaml"))
	require.FileExists(t, filepath.Join(folderDir, folderMeta))
	require.FileExists(t, filepath.Join(folderDir, "get-user.qv.yaml"))
	require.FileExists(t, filepath.Join(dir, environmentsDir, "local.qv.yaml"))
}

// TestSaveKeepsWorkspaceRootEntries pins the other half of the contract: the root is not
// managed, so it is the safe place for files that are not part of the model.
func TestSaveKeepsWorkspaceRootEntries(t *testing.T) {
	ctx := context.Background()
	ws := sampleWorkspace()
	dir := t.TempDir()
	require.NoError(t, Save(ctx, ws, dir))

	rootFiles := []string{
		filepath.Join(dir, "README.md"),
		filepath.Join(dir, "Makefile"),
	}
	rootDirs := []string{
		filepath.Join(dir, ".quiver"),
		filepath.Join(dir, ".github", "workflows"),
	}
	for _, p := range rootFiles {
		require.NoError(t, os.WriteFile(p, []byte("keep me"), 0o644))
	}
	for _, p := range rootDirs {
		require.NoError(t, os.MkdirAll(p, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(p, "keep.txt"), []byte("keep me"), 0o644))
	}

	require.NoError(t, Save(ctx, ws, dir))

	for _, p := range rootFiles {
		require.FileExistsf(t, p, "root file %s must survive", p)
	}
	for _, p := range rootDirs {
		require.FileExistsf(t, filepath.Join(p, "keep.txt"), "root dir %s must survive intact", p)
	}
	require.FileExists(t, filepath.Join(dir, ".gitignore"))
	require.FileExists(t, filepath.Join(dir, manifestFile))
}

func TestSaveDedupesFilenames(t *testing.T) {
	ws := &model.Workspace{
		SchemaVersion: currentSchemaVersion, ID: "ws", Name: "n",
		Collections: []model.Collection{{ID: "c", Name: "C", Items: []model.Item{
			{Request: &model.Request{ID: "1", Name: "Dup", Method: "GET"}},
			{Request: &model.Request{ID: "2", Name: "Dup", Method: "GET"}},
		}}},
	}
	dir := t.TempDir()
	require.NoError(t, Save(context.Background(), ws, dir))

	cdir := filepath.Join(dir, collectionsDir, "c")
	require.FileExists(t, filepath.Join(cdir, "dup.qv.yaml"))
	require.FileExists(t, filepath.Join(cdir, "dup-2.qv.yaml"))
}

func TestSaveReservedRequestNames(t *testing.T) {
	// Requests whose names slug to the reserved metadata bases must not clobber the
	// container's own collection.qv.yaml / folder.qv.yaml, and must survive a round-trip.
	ws := &model.Workspace{
		SchemaVersion: currentSchemaVersion, ID: "ws", Name: "n",
		Collections: []model.Collection{{ID: "c", Name: "C", Items: []model.Item{
			{Request: &model.Request{ID: "r1", Name: "Collection", Method: "GET"}},
			{Request: &model.Request{ID: "r2", Name: "Folder", Method: "GET"}},
			{Request: &model.Request{ID: "r3", Name: "Ping", Method: "GET"}},
		}}},
	}
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, Save(ctx, ws, dir))

	cdir := filepath.Join(dir, collectionsDir, "c")
	require.FileExists(t, filepath.Join(cdir, "collection-2.qv.yaml"))
	require.FileExists(t, filepath.Join(cdir, "folder-2.qv.yaml"))

	got, err := Load(ctx, dir)
	require.NoError(t, err)
	require.Len(t, got.Collections[0].Items, 3, "all requests survive the round-trip")
}

func TestInit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	ws, err := Init(ctx, dir, "My WS")
	require.NoError(t, err)
	require.NotEmpty(t, ws.ID)
	require.Equal(t, "My WS", ws.Name)

	loaded, err := Load(ctx, dir)
	require.NoError(t, err)
	require.Equal(t, ws.ID, loaded.ID)
	require.NoError(t, loaded.Validate())
	require.FileExists(t, filepath.Join(dir, ".gitignore"))
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Get user":    "get-user",
		"Users API":   "users-api",
		"  Hello!!  ": "hello",
		"a/b\\c":      "a-b-c",
		"MiXeD":       "mixed",
		"":            "item",
		"___":         "item",
	}
	for in, want := range cases {
		require.Equalf(t, want, slugify(in), "slugify(%q)", in)
	}
}
