// Package helpgate answers community-transfer's help requests before any
// other package of the program is initialized.
//
// The community packages that community-transfer imports load the .env
// configuration and open the database and Redis connections in their init()
// functions, which run before main(). Printing help from main() was therefore
// too late: `community-transfer --help` first read the database configuration
// and connected to the target database (or logged a connection error).
//
// This package only depends on the standard library and its import path
// ("github.com/kasuganosora/community/cmd/...") sorts before every other
// community package, so the Go package initialization order (dependencies
// first, then by import path) runs its init() before config, db, db/database
// and db/redis. Keep it free of non-standard-library imports.
package helpgate

import (
	_ "embed"
	"fmt"
	"io"
	"os"
)

// Usage is the generated `flag.PrintDefaults` output of community-transfer.
// TestHelpUsageMatchesFlags in package main keeps it in sync; regenerate it
// with `UPDATE_USAGE=1 go test ./cmd/community-transfer -run TestHelpUsageMatchesFlags`.
//
//go:embed usage.txt
var Usage string

// Requested reports whether args (without the program name) only ask for
// help: a leading "help" command, or -h/-help/--help anywhere before a "--"
// terminator (the same rule tootctl uses).
func Requested(args []string) bool {
	if len(args) > 0 && args[0] == "help" {
		return true
	}
	for _, a := range args {
		switch a {
		case "--":
			return false
		case "-h", "--h", "-help", "--help":
			return true
		}
	}
	return false
}

// Print writes the full help text.
func Print(w io.Writer, prog string) {
	fmt.Fprintf(w, "Usage of %s:\n%s", prog, Usage)
}

func init() {
	if Requested(os.Args[1:]) {
		Print(os.Stdout, "community-transfer")
		os.Exit(0)
	}
}
