package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gsoares85/quiver/internal/buildinfo"
)

func TestVersionCommand(t *testing.T) {
	var out, errOut bytes.Buffer

	code := run([]string{"version"}, &out, &errOut)

	require.Equal(t, 0, code)
	require.Empty(t, errOut.String())
	require.Equal(t, buildinfo.Get().String()+"\n", out.String())
}

func TestVersionRejectsArgs(t *testing.T) {
	var out, errOut bytes.Buffer

	code := run([]string{"version", "extra"}, &out, &errOut)

	require.Equal(t, 1, code)
	require.Contains(t, errOut.String(), "error:")
}
