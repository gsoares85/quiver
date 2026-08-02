package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootNoArgsShowsHelp(t *testing.T) {
	var out, errOut bytes.Buffer

	code := run([]string{}, &out, &errOut)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "quiver")
	require.Contains(t, out.String(), "version") // the subcommand is listed
	require.Empty(t, errOut.String())
}

func TestUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer

	code := run([]string{"definitely-not-a-command"}, &out, &errOut)

	require.Equal(t, 1, code)
	require.Contains(t, errOut.String(), "error:")
}
