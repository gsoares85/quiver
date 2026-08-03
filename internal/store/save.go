package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gsoares85/quiver/internal/model"
)

// Init creates a new, empty workspace at path (a fresh ULID, the given name) and
// writes it to disk, returning the created workspace.
func Init(ctx context.Context, path, name string) (*model.Workspace, error) {
	ws := &model.Workspace{SchemaVersion: currentSchemaVersion, ID: model.NewID(), Name: name}
	if err := Save(ctx, ws, path); err != nil {
		return nil, err
	}
	return ws, nil
}

// Save writes ws to the git-native tree rooted at path. Filenames are canonical slugs
// of entity names (de-duplicated within a parent); each parent records its children's
// authored order. Writes are atomic, and files/dirs no longer present in the model are
// pruned. Saving a workspace loaded from a canonical tree reproduces it byte-for-byte.
func Save(ctx context.Context, ws *model.Workspace, path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create workspace dir: %w", err)
	}

	// Collection directories, in workspace order, recorded in the manifest.
	colDirs := make([]string, len(ws.Collections))
	usedCols := map[string]bool{}
	for i := range ws.Collections {
		colDirs[i] = uniqueBase(slugify(ws.Collections[i].Name), usedCols)
	}

	manifest := fileManifest{
		SchemaVersion: currentSchemaVersion,
		ID:            ws.ID,
		Name:          ws.Name,
		Settings:      ws.Settings,
		Variables:     ws.Variables,
		Order:         colDirs,
	}
	if err := writeYAMLFile(ctx, filepath.Join(path, manifestFile), manifest); err != nil {
		return err
	}

	if err := saveEnvironments(ctx, filepath.Join(path, environmentsDir), ws.Environments); err != nil {
		return err
	}

	colRoot := filepath.Join(path, collectionsDir)
	for i := range ws.Collections {
		if err := saveCollection(ctx, filepath.Join(colRoot, colDirs[i]), &ws.Collections[i]); err != nil {
			return err
		}
	}
	if err := pruneDir(colRoot, toSet(colDirs)); err != nil {
		return err
	}

	return ensureGitignore(path)
}

func saveEnvironments(ctx context.Context, dir string, envs []model.Environment) error {
	if len(envs) > 0 {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create environments dir: %w", err)
		}
	}
	kept := map[string]bool{}
	used := map[string]bool{}
	for i := range envs {
		name := uniqueBase(slugify(envs[i].Name), used) + qvExt
		if err := writeYAMLFile(ctx, filepath.Join(dir, name), newFileEnvironment(envs[i])); err != nil {
			return err
		}
		kept[name] = true
	}
	return pruneDir(dir, kept)
}

func saveCollection(ctx context.Context, dir string, c *model.Collection) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create collection dir: %w", err)
	}
	order, kept, err := saveItems(ctx, dir, c.Items)
	if err != nil {
		return err
	}
	meta := fileCollection{
		SchemaVersion: currentSchemaVersion,
		ID:            c.ID,
		Name:          c.Name,
		Auth:          c.Auth,
		Variables:     c.Variables,
		PreRequest:    c.PreRequest,
		Test:          c.Test,
		Order:         order,
	}
	if err := writeYAMLFile(ctx, filepath.Join(dir, collectionMeta), meta); err != nil {
		return err
	}
	kept[collectionMeta] = true
	return pruneDir(dir, kept)
}

func saveFolder(ctx context.Context, dir string, f *model.Folder) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create folder dir: %w", err)
	}
	order, kept, err := saveItems(ctx, dir, f.Items)
	if err != nil {
		return err
	}
	meta := fileFolder{
		SchemaVersion: currentSchemaVersion,
		ID:            f.ID,
		Name:          f.Name,
		Auth:          f.Auth,
		Variables:     f.Variables,
		PreRequest:    f.PreRequest,
		Test:          f.Test,
		Order:         order,
	}
	if err := writeYAMLFile(ctx, filepath.Join(dir, folderMeta), meta); err != nil {
		return err
	}
	kept[folderMeta] = true
	return pruneDir(dir, kept)
}

// saveItems writes each item under dir and returns the authored order of child names
// (folder dir names and request file names) and the set of names to keep when pruning.
func saveItems(ctx context.Context, dir string, items []model.Item) (order []string, kept map[string]bool, err error) {
	kept = map[string]bool{}
	usedFolders := map[string]bool{}
	usedRequests := map[string]bool{}
	for i := range items {
		switch {
		case items[i].Folder != nil:
			name := uniqueBase(slugify(items[i].Folder.Name), usedFolders)
			if err = saveFolder(ctx, filepath.Join(dir, name), items[i].Folder); err != nil {
				return nil, nil, err
			}
			order = append(order, name)
			kept[name] = true
		case items[i].Request != nil:
			name := uniqueBase(slugify(items[i].Request.Name), usedRequests) + qvExt
			if err = writeYAMLFile(ctx, filepath.Join(dir, name), newFileRequest(*items[i].Request)); err != nil {
				return nil, nil, err
			}
			order = append(order, name)
			kept[name] = true
		}
	}
	return order, kept, nil
}

// writeYAMLFile encodes v and atomically writes it to path.
func writeYAMLFile(ctx context.Context, path string, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := marshalYAML(v)
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	if err := atomicWriteFile(path, data); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// pruneDir removes any entry of dir not present in keep. A missing directory is fine.
func pruneDir(dir string, keep map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("prune %s: %w", filepath.Base(dir), err)
	}
	for _, e := range entries {
		if keep[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("prune %s: %w", e.Name(), err)
		}
	}
	return nil
}

// uniqueBase returns base, or base-2, base-3, … so it is unique within used, recording
// the chosen name in used.
func uniqueBase(base string, used map[string]bool) string {
	name := base
	for i := 2; used[name]; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	used[name] = true
	return name
}

// slugify turns an entity name into a filesystem-safe, lowercase slug ("Get user" ->
// "get-user"). Runs of non-alphanumeric characters collapse to a single hyphen; an
// empty result becomes "item".
func slugify(name string) string {
	var b strings.Builder
	pendingHyphen := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pendingHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingHyphen = false
			b.WriteRune(r)
		} else {
			pendingHyphen = true
		}
	}
	if b.Len() == 0 {
		return "item"
	}
	return b.String()
}

func toSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}
