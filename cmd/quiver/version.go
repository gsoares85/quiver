package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gsoares85/quiver/internal/buildinfo"
)

// newVersionCmd prints the binary's version and build metadata.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), buildinfo.Get().String())
			return err
		},
	}
}
