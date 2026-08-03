package store

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// marshalYAML encodes v as byte-stable YAML: 2-space indent, LF line endings, and a
// trailing newline. Struct fields serialize in declaration order, so the same value
// always produces the same bytes — the property that keeps git diffs minimal.
func marshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close yaml encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// unmarshalYAML decodes YAML bytes into v.
func unmarshalYAML(data []byte, v any) error {
	if err := yaml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode yaml: %w", err)
	}
	return nil
}
