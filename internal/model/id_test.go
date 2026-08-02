package model

import (
	"errors"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
)

// fixedReader yields a deterministic, repeating byte stream so tests produce
// reproducible identifiers without real randomness.
type fixedReader struct{ next byte }

func (r *fixedReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.next
		r.next++
	}
	return len(p), nil
}

// errReader always fails, simulating a broken entropy source.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("entropy failure") }

func TestNewIDIsValidULID(t *testing.T) {
	id := NewID()

	require.Len(t, id, 26)
	_, err := ulid.Parse(id)
	require.NoError(t, err)
}

func TestGeneratorIsDeterministic(t *testing.T) {
	at := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	g1 := newGeneratorWith(func() time.Time { return at }, &fixedReader{})
	g2 := newGeneratorWith(func() time.Time { return at }, &fixedReader{})

	require.Equal(t, g1.New(), g2.New())
}

func TestGeneratorSortsByTime(t *testing.T) {
	entropy := &fixedReader{}
	early := newGeneratorWith(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }, entropy)
	late := newGeneratorWith(func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }, entropy)

	// ULIDs encode the timestamp in their high bits, so an earlier time sorts first.
	require.Less(t, early.New(), late.New())
}

func TestGeneratorTryNew(t *testing.T) {
	id, err := newGeneratorWith(time.Now, &fixedReader{}).TryNew()

	require.NoError(t, err)
	require.Len(t, id, 26)
}

func TestGeneratorTryNewReturnsEntropyError(t *testing.T) {
	_, err := newGeneratorWith(time.Now, errReader{}).TryNew()

	require.Error(t, err)
}

func TestGeneratorNewPanicsOnEntropyError(t *testing.T) {
	g := newGeneratorWith(time.Now, errReader{})

	require.Panics(t, func() { _ = g.New() })
}
