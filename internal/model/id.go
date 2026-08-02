package model

import (
	"crypto/rand"
	"io"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Generator produces ULID identifiers. ULIDs are lexicographically sortable by
// creation time, which keeps git-native diffs stable and merges clean. A Generator
// is safe for concurrent use; construct one with NewGenerator (or use NewID).
type Generator struct {
	mu      sync.Mutex
	now     func() time.Time
	entropy io.Reader
}

// NewGenerator returns a Generator backed by the real clock and crypto/rand entropy.
func NewGenerator() *Generator {
	return &Generator{now: time.Now, entropy: rand.Reader}
}

// newGeneratorWith builds a Generator with an injected clock and entropy source. It
// exists so tests can produce deterministic identifiers.
func newGeneratorWith(now func() time.Time, entropy io.Reader) *Generator {
	return &Generator{now: now, entropy: entropy}
}

// TryNew returns a new ULID as a string, or an error if the entropy source fails. It
// is safe to call concurrently.
func (g *Generator) TryNew() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	id, err := ulid.New(ulid.Timestamp(g.now()), g.entropy)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// New returns a new ULID as a string. It is safe to call concurrently and panics only
// if the entropy source fails — which the default crypto/rand source never does. Use
// TryNew to handle entropy errors from a custom source.
func (g *Generator) New() string {
	id, err := g.TryNew()
	if err != nil {
		panic(err)
	}
	return id
}

// defaultGenerator backs the package-level NewID helper.
var defaultGenerator = NewGenerator()

// NewID returns a new ULID string from the default generator.
func NewID() string {
	return defaultGenerator.New()
}
