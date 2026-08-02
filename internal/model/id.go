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

// New returns a new ULID as a string. It is safe to call concurrently.
func (g *Generator) New() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return ulid.MustNew(ulid.Timestamp(g.now()), g.entropy).String()
}

// defaultGenerator backs the package-level NewID helper.
var defaultGenerator = NewGenerator()

// NewID returns a new ULID string from the default generator.
func NewID() string {
	return defaultGenerator.New()
}
