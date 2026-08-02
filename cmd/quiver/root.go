package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newRootCmd builds the quiver command tree. Output streams are injected so the CLI
// is fully testable without touching the process's stdio.
func newRootCmd(out, errOut io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "quiver",
		Short: "Quiver — a local-first, git-native API client",
		Long: "Quiver runs API collections from the terminal and in CI, using the same " +
			"engine as the desktop app.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.AddCommand(newVersionCmd())
	return root
}

// run executes the CLI with the given args and streams and returns a process exit
// code. It holds the only error-to-exit-code mapping, keeping main() trivial.
func run(args []string, out, errOut io.Writer) int {
	root := newRootCmd(out, errOut)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(errOut, "error:", err)
		return 1
	}
	return 0
}
