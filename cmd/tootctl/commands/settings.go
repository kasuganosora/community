package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/controllers/api/admin"
	"github.com/urfave/cli/v2"
)

// SettingsCommand implements tootctl settings subcommands.
func SettingsCommand() *cli.Command {
	return &cli.Command{
		Name:        "settings",
		Usage:       "Manage instance settings",
		Subcommands: []*cli.Command{SettingsImportEnvCommand()},
	}
}

// SettingsImportEnvCommand copies admin-editable settings from .env into the
// database so they can be managed in the admin UI.
func SettingsImportEnvCommand() *cli.Command {
	return &cli.Command{
		Name:  "import-env",
		Usage: "Copy non-core .env settings into the database",
		Description: `Reads the .env file (ENV_FILE, default /app/config/.env), saves every
setting that the admin UI can edit into the database and prints the variable
names to remove afterwards. Database, Redis, domain and secret keys stay in .env.
Remove the printed variables from BOTH the host .env used as the compose
env_file AND /app/config/.env: env_file entries become process environment
variables, which keep overriding the database until removed.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "dry-run", Usage: "show what would be imported without saving"},
			&cli.StringFlag{Name: "env-file", Usage: "path to the .env file (default: the active ENV_FILE)"},
		},
		Action: func(c *cli.Context) error {
			path := c.String("env-file")
			if path == "" {
				path = config.EnvFile()
			}
			items, err := admin.PlanEnvImport(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			return runSettingsImport(c.App.Writer, path, items, c.Bool("dry-run"), admin.ApplyEnvImport, os.LookupEnv)
		},
	}
}

func runSettingsImport(out interface{ Write([]byte) (int, error) }, path string, items []admin.EnvImportItem, dryRun bool, apply func([]admin.EnvImportItem) error, lookup func(string) (string, bool)) error {
	if len(items) == 0 {
		fmt.Fprintf(out, "No importable settings found in %s.\n", path)
		return nil
	}
	var ok, bad []admin.EnvImportItem
	for _, item := range items {
		if item.Err != nil {
			bad = append(bad, item)
		} else {
			ok = append(ok, item)
		}
	}
	verb := "Importing"
	if dryRun {
		verb = "Would import"
	}
	fmt.Fprintf(out, "%s %d setting(s) from %s:\n", verb, len(ok), path)
	for _, item := range ok {
		value := fmt.Sprint(item.Value)
		if item.Secret {
			value = "(secret)"
		}
		fmt.Fprintf(out, "  %-28s -> %s = %s\n", item.Env, item.Key, value)
	}
	for _, item := range bad {
		fmt.Fprintf(out, "  SKIP %-23s %v\n", item.Env, item.Err)
	}
	if !dryRun {
		if err := apply(ok); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
	}
	if len(ok) > 0 {
		names := make([]string, 0, len(ok))
		for _, item := range ok {
			names = append(names, item.Env)
		}
		if dryRun {
			fmt.Fprintln(out, "\nAfter a real import, remove these variables:")
		} else {
			fmt.Fprintln(out, "\nSaved. Now remove these variables:")
		}
		for _, name := range names {
			fmt.Fprintf(out, "  %s\n", name)
		}
		fmt.Fprintln(out, "Remove them from BOTH files, then recreate the container (docker compose up -d):")
		fmt.Fprintln(out, "  - the host .env used by docker compose (env_file), and")
		fmt.Fprintf(out, "  - %s\n", path)
		fmt.Fprintln(out, "env_file entries become process environment variables, and those override the")
		fmt.Fprintln(out, "database even after the line is gone from the second file.")
		var still []string
		for _, name := range names {
			if v, set := lookup(name); set && strings.TrimSpace(v) != "" {
				still = append(still, name)
			}
		}
		if len(still) > 0 {
			fmt.Fprintf(out, "\nWARNING: still set in this process environment (so they override the database): %s\n", strings.Join(still, ", "))
			fmt.Fprintln(out, "Remove them from the host compose .env (env_file) and recreate the container.")
		}
	}
	return nil
}
