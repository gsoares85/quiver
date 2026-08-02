// Package runner orchestrates running a collection, folder, or request through the
// shared engine and script host — resolving variables and secrets, executing with
// bounded parallelism, and emitting lifecycle events that reporters consume. It is
// the shared core behind both the desktop app and the quiver CLI.
package runner
