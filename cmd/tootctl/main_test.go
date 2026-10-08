package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

func runTootctl(t *testing.T, args ...string) (string, bool) {
	t.Helper()

	prepared := false
	app := newApp("9.9.9", func() { prepared = true })

	var out bytes.Buffer
	app.Writer = &out
	app.ErrWriter = &out

	require.NotPanics(t, func() {
		require.NoError(t, app.Run(append([]string{"tootctl"}, args...)))
	}, "tootctl %s", strings.Join(args, " "))

	return out.String(), prepared
}

func TestRootHelpAndVersion(t *testing.T) {
	out, prepared := runTootctl(t, "--help")
	require.Contains(t, out, "tootctl - Community server command-line interface")
	require.Contains(t, out, "--verbose, -v")
	require.Contains(t, out, "--version")
	require.False(t, prepared, "help must not run database migrations")

	out, _ = runTootctl(t, "-v", "--help")
	require.Contains(t, out, "USAGE:")

	out, prepared = runTootctl(t, "--version")
	require.Contains(t, out, "9.9.9")
	require.False(t, prepared)

	out, prepared = runTootctl(t, "help", "accounts")
	require.Contains(t, out, "tootctl accounts - Manage accounts")
	require.False(t, prepared)
}

// TestEveryCommandHelp builds the flag set of every (sub)command, which is
// where duplicate flag names panic with "flag redefined".
func TestEveryCommandHelp(t *testing.T) {
	var walk func(path []string, cmds []*cli.Command)

	walk = func(path []string, cmds []*cli.Command) {
		for _, cmd := range cmds {
			if cmd.Name == "help" {
				continue
			}

			args := append(append([]string{}, path...), cmd.Name)
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				out, prepared := runTootctl(t, append(args, "--help")...)
				require.Contains(t, out, "tootctl "+strings.Join(args, " "))
				require.False(t, prepared)

				out, _ = runTootctl(t, append([]string{"-v"}, append(args, "-h")...)...)
				require.Contains(t, out, "USAGE:")
			})

			walk(args, cmd.Subcommands)
		}
	}

	walk(nil, newApp("9.9.9", nil).Commands)
}

func TestNeedsDatabase(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"help"}, false},
		{[]string{"h", "accounts"}, false},
		{[]string{"accounts", "create", "--help"}, false},
		{[]string{"media", "usage", "-h"}, false},
		{[]string{"version"}, true},
		{[]string{"accounts", "create", "--username", "alice"}, true},
		{[]string{"accounts", "modify", "--", "-h"}, true},
	} {
		require.Equal(t, tc.want, needsDatabase(tc.args), "%v", tc.args)
	}
}
