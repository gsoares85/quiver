// Command quiver is the Quiver CLI: a single static binary that runs API collections
// from the terminal and in CI, using the same engine as the desktop app. No request
// or assertion logic lives here — the CLI is a thin shell over internal/*.
package main

import "os"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
