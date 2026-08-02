// Package script runs user pre-request and test scripts in a sandboxed JavaScript
// host (goja). The global scope starts empty; only the curated qv API is injected,
// so scripts have no ambient authority and run identically in the app and the CLI.
package script
