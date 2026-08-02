// Package buildinfo exposes the binary's version and build metadata. Values are set
// at build time via -ldflags and fall back to development-friendly placeholders, so
// the package is safe to use under `go run` and in tests.
package buildinfo

import (
	"fmt"
	"runtime"
)

// Build-time variables, injected by the linker, e.g.:
//
//	go build -ldflags "\
//	  -X github.com/gsoares85/quiver/internal/buildinfo.version=v1.2.3 \
//	  -X github.com/gsoares85/quiver/internal/buildinfo.commit=abc1234 \
//	  -X github.com/gsoares85/quiver/internal/buildinfo.date=2026-08-02"
//
// The version normally comes from the release tag (`git describe --tags`). They are
// unexported so callers go through Get(), keeping the surface small and testable.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Info is a snapshot of the binary's build metadata.
type Info struct {
	Version string // release version (e.g. "v1.2.3"), or "dev"
	Commit  string // short git commit SHA, or "none"
	Date    string // build date, or "unknown"
	Go      string // Go toolchain the binary was built with
	OS      string // target operating system
	Arch    string // target architecture
}

// Get returns the current build metadata, combining the linker-injected values with
// the runtime's Go version and target platform.
func Get() Info {
	return Info{
		Version: version,
		Commit:  commit,
		Date:    date,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}
}

// String renders the metadata as a single human-readable line, e.g.
// "quiver v1.2.3 (abc1234, built 2026-08-02, go1.25 darwin/arm64)".
func (i Info) String() string {
	return fmt.Sprintf("quiver %s (%s, built %s, %s %s/%s)",
		i.Version, i.Commit, i.Date, i.Go, i.OS, i.Arch)
}
