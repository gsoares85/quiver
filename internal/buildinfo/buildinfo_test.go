package buildinfo

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetDefaults(t *testing.T) {
	got := Get()

	require.Equal(t, "dev", got.Version)
	require.Equal(t, "none", got.Commit)
	require.Equal(t, "unknown", got.Date)
	require.Equal(t, runtime.Version(), got.Go)
	require.Equal(t, runtime.GOOS, got.OS)
	require.Equal(t, runtime.GOARCH, got.Arch)
}

func TestGetReflectsInjectedValues(t *testing.T) {
	// The linker injects these at build time; simulate that by setting the
	// package-level variables (reachable from the same package) and restoring them.
	defer func(v, c, d string) { version, commit, date = v, c, d }(version, commit, date)
	version, commit, date = "v1.2.3", "abc1234", "2026-08-02"

	got := Get()

	require.Equal(t, "v1.2.3", got.Version)
	require.Equal(t, "abc1234", got.Commit)
	require.Equal(t, "2026-08-02", got.Date)
}

func TestInfoString(t *testing.T) {
	tests := []struct {
		name string
		in   Info
		want string
	}{
		{
			name: "release",
			in:   Info{Version: "v1.2.3", Commit: "abc1234", Date: "2026-08-02", Go: "go1.25", OS: "darwin", Arch: "arm64"},
			want: "quiver v1.2.3 (abc1234, built 2026-08-02, go1.25 darwin/arm64)",
		},
		{
			name: "dev defaults",
			in:   Info{Version: "dev", Commit: "none", Date: "unknown", Go: "go1.25", OS: "linux", Arch: "amd64"},
			want: "quiver dev (none, built unknown, go1.25 linux/amd64)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.in.String())
		})
	}
}
