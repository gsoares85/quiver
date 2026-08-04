package store

import "github.com/gsoares85/quiver/internal/model"

// currentSchemaVersion is the on-disk format version written into every file. Bumping
// it requires a registered migration and golden tests (see migrate.go).
const currentSchemaVersion = 1

// The file* types are the on-disk projections of the model. Each carries its own
// schemaVersion (a storage concern the model does not track), and container files
// (manifest, collection, folder) carry an `order` list because the filesystem cannot
// express the authored order of their children.

// fileManifest is the on-disk shape of quiver.yaml, the workspace root. Collections
// and environments live in sibling directories/files; only the collection-directory
// order is recorded here.
type fileManifest struct {
	SchemaVersion int              `yaml:"schemaVersion"`
	ID            string           `yaml:"id"`
	Name          string           `yaml:"name"`
	Settings      model.Settings   `yaml:"settings"`
	Variables     []model.Variable `yaml:"variables,omitempty"`
	Order         []string         `yaml:"order,omitempty"`
}

// fileEnvironment is the on-disk shape of environments/<name>.qv.yaml.
type fileEnvironment struct {
	SchemaVersion     int `yaml:"schemaVersion"`
	model.Environment `yaml:",inline"`
}

// fileCollection is the on-disk shape of a collection's collection.qv.yaml; its items
// live as sibling files/dirs, ordered by Order.
type fileCollection struct {
	SchemaVersion int              `yaml:"schemaVersion"`
	ID            string           `yaml:"id"`
	Name          string           `yaml:"name"`
	Auth          *model.Auth      `yaml:"auth,omitempty"`
	Variables     []model.Variable `yaml:"variables,omitempty"`
	PreRequest    string           `yaml:"preRequest,omitempty"`
	Test          string           `yaml:"test,omitempty"`
	Order         []string         `yaml:"order,omitempty"`
}

// fileFolder is the on-disk shape of a folder's folder.qv.yaml.
type fileFolder struct {
	SchemaVersion int              `yaml:"schemaVersion"`
	ID            string           `yaml:"id"`
	Name          string           `yaml:"name"`
	Auth          *model.Auth      `yaml:"auth,omitempty"`
	Variables     []model.Variable `yaml:"variables,omitempty"`
	PreRequest    string           `yaml:"preRequest,omitempty"`
	Test          string           `yaml:"test,omitempty"`
	Order         []string         `yaml:"order,omitempty"`
}

// fileRequest is the on-disk shape of a request's <name>.qv.yaml.
type fileRequest struct {
	SchemaVersion int `yaml:"schemaVersion"`
	model.Request `yaml:",inline"`
}

func newFileRequest(r model.Request) fileRequest {
	return fileRequest{SchemaVersion: currentSchemaVersion, Request: r}
}

func newFileEnvironment(e model.Environment) fileEnvironment {
	return fileEnvironment{SchemaVersion: currentSchemaVersion, Environment: e}
}
