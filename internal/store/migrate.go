package store

import "fmt"

// migration transforms a parsed file document from one schema version to the next.
type migration func(doc map[string]any) (map[string]any, error)

// migrations holds the registered upgrade steps: migrations[v] upgrades a document from
// schema version v to v+1. It is empty today because v1 is both the first and the
// current on-disk version; raising currentSchemaVersion adds an entry here together with
// a golden test.
var migrations = []migration{}

// docVersion reports the schemaVersion of a parsed document (0 when absent or not a
// number).
func docVersion(doc map[string]any) int {
	switch v := doc["schemaVersion"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// applyMigrations upgrades doc to the current schema version using migs, running each
// (vN) -> (vN+1) step in turn. It errors if the document is newer than supported, a
// required step is missing, or a step fails to advance the version.
func applyMigrations(doc map[string]any, migs []migration) (map[string]any, error) {
	v := docVersion(doc)
	if v > currentSchemaVersion {
		return nil, fmt.Errorf("schema version %d is newer than supported version %d", v, currentSchemaVersion)
	}
	for v < currentSchemaVersion {
		if v < 0 || v >= len(migs) || migs[v] == nil {
			return nil, fmt.Errorf("no migration registered from schema version %d", v)
		}
		next, err := migs[v](doc)
		if err != nil {
			return nil, fmt.Errorf("migrate v%d to v%d: %w", v, v+1, err)
		}
		doc = next
		nv := docVersion(doc)
		if nv <= v {
			return nil, fmt.Errorf("migration from v%d did not advance the schema version", v)
		}
		v = nv
	}
	return doc, nil
}
