package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gsoares85/quiver/internal/model"
)

const (
	manifestFile    = "quiver.yaml"
	collectionMeta  = "collection.qv.yaml"
	folderMeta      = "folder.qv.yaml"
	qvExt           = ".qv.yaml"
	environmentsDir = "environments"
	collectionsDir  = "collections"
)

// Load reads the git-native workspace rooted at path into a model.Workspace. The tree
// maps directly onto the filesystem: collections and folders are directories, requests
// are files, and authored order is taken from each parent's `order` list (falling back
// to alphabetical).
func Load(ctx context.Context, path string) (*model.Workspace, error) {
	var man fileManifest
	if err := loadFile(ctx, filepath.Join(path, manifestFile), &man); err != nil {
		return nil, fmt.Errorf("load workspace manifest: %w", err)
	}
	ws := &model.Workspace{
		SchemaVersion: man.SchemaVersion,
		ID:            man.ID,
		Name:          man.Name,
		Settings:      man.Settings,
		Variables:     man.Variables,
	}

	envs, err := loadEnvironments(ctx, filepath.Join(path, environmentsDir))
	if err != nil {
		return nil, err
	}
	ws.Environments = envs

	cols, err := loadCollections(ctx, filepath.Join(path, collectionsDir), man.Order)
	if err != nil {
		return nil, err
	}
	ws.Collections = cols

	return ws, nil
}

// loadFile reads a single .qv.yaml file, migrates it to the current schema version if
// needed, and decodes it into out.
func loadFile(ctx context.Context, path string, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	base := filepath.Base(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", base, err)
	}

	// Peek the schema version cheaply before deciding how to decode.
	var head struct {
		SchemaVersion int `yaml:"schemaVersion"`
	}
	if err := unmarshalYAML(data, &head); err != nil {
		return fmt.Errorf("parse %s: %w", base, err)
	}
	switch {
	case head.SchemaVersion > currentSchemaVersion:
		return fmt.Errorf("%s: schema version %d is newer than supported version %d", base, head.SchemaVersion, currentSchemaVersion)
	case head.SchemaVersion == currentSchemaVersion:
		if err := unmarshalYAML(data, out); err != nil {
			return fmt.Errorf("decode %s: %w", base, err)
		}
		return nil
	}

	// Older format: migrate through the generic representation, then decode.
	var doc map[string]any
	if err := unmarshalYAML(data, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", base, err)
	}
	migrated, err := applyMigrations(doc, migrations)
	if err != nil {
		return fmt.Errorf("migrate %s: %w", base, err)
	}
	remarshaled, err := marshalYAML(migrated)
	if err != nil {
		return fmt.Errorf("re-encode %s: %w", base, err)
	}
	if err := unmarshalYAML(remarshaled, out); err != nil {
		return fmt.Errorf("decode %s: %w", base, err)
	}
	return nil
}

func loadEnvironments(ctx context.Context, dir string) ([]model.Environment, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // a workspace without environments is valid
	}
	if err != nil {
		return nil, fmt.Errorf("read environments: %w", err)
	}
	var envs []model.Environment
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), qvExt) {
			continue
		}
		var fe fileEnvironment
		if err := loadFile(ctx, filepath.Join(dir, e.Name()), &fe); err != nil {
			return nil, err
		}
		envs = append(envs, fe.Environment)
	}
	return envs, nil
}

func loadCollections(ctx context.Context, dir string, order []string) ([]model.Collection, error) {
	names, err := childDirs(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read collections: %w", err)
	}
	var cols []model.Collection
	for _, name := range orderNames(names, order) {
		c, err := loadCollection(ctx, filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, nil
}

func loadCollection(ctx context.Context, dir string) (model.Collection, error) {
	var fc fileCollection
	if err := loadFile(ctx, filepath.Join(dir, collectionMeta), &fc); err != nil {
		return model.Collection{}, err
	}
	items, err := loadItems(ctx, dir, fc.Order)
	if err != nil {
		return model.Collection{}, err
	}
	return model.Collection{
		ID:         fc.ID,
		Name:       fc.Name,
		Auth:       fc.Auth,
		Variables:  fc.Variables,
		PreRequest: fc.PreRequest,
		Test:       fc.Test,
		Items:      items,
	}, nil
}

func loadFolder(ctx context.Context, dir string) (model.Folder, error) {
	var ff fileFolder
	if err := loadFile(ctx, filepath.Join(dir, folderMeta), &ff); err != nil {
		return model.Folder{}, err
	}
	items, err := loadItems(ctx, dir, ff.Order)
	if err != nil {
		return model.Folder{}, err
	}
	return model.Folder{
		ID:         ff.ID,
		Name:       ff.Name,
		Auth:       ff.Auth,
		Variables:  ff.Variables,
		PreRequest: ff.PreRequest,
		Test:       ff.Test,
		Items:      items,
	}, nil
}

// loadItems reads the child folders (subdirectories) and requests (*.qv.yaml files,
// excluding the container's own metadata) of dir, ordered by order then alphabetically.
func loadItems(ctx context.Context, dir string, order []string) ([]model.Item, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read items in %s: %w", filepath.Base(dir), err)
	}
	isDir := map[string]bool{}
	var names []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.IsDir():
			names = append(names, name)
			isDir[name] = true
		case strings.HasSuffix(name, qvExt) && name != collectionMeta && name != folderMeta:
			names = append(names, name)
		}
	}

	var items []model.Item
	for _, name := range orderNames(names, order) {
		if isDir[name] {
			f, err := loadFolder(ctx, filepath.Join(dir, name))
			if err != nil {
				return nil, err
			}
			items = append(items, model.Item{Folder: &f})
		} else {
			var fr fileRequest
			if err := loadFile(ctx, filepath.Join(dir, name), &fr); err != nil {
				return nil, err
			}
			r := fr.Request
			items = append(items, model.Item{Request: &r})
		}
	}
	return items, nil
}

func childDirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// orderNames returns names sorted by the order list first (entries that exist, in
// order), then any remaining names alphabetically. It is deterministic.
func orderNames(names, order []string) []string {
	present := make(map[string]bool, len(names))
	for _, n := range names {
		present[n] = true
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, n := range order {
		if present[n] && !seen[n] {
			out = append(out, n)
			seen[n] = true
		}
	}
	rest := make([]string, 0, len(names))
	for _, n := range names {
		if !seen[n] {
			rest = append(rest, n)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}
