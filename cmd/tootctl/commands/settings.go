package commands

import (
	"fmt"

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
setting that the admin UI can edit into the database and prints the .env lines
that can then be removed. Database, Redis, domain and secret keys stay in .env.
Values in .env keep overriding the database until those lines are removed.`,
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
			return runSettingsImport(c.App.Writer, path, items, c.Bool("dry-run"), admin.ApplyEnvImport)
		},
	}
}

func runSettingsImport(out interface{ Write([]byte) (int, error) }, path string, items []admin.EnvImportItem, dryRun bool, apply func([]admin.EnvImportItem) error) error {
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
		if dryRun {
			fmt.Fprintln(out, "\nAfter a real import, these lines can be removed from .env:")
		} else {
			fmt.Fprintln(out, "\nSaved. These lines can now be removed from .env (then restart):")
		}
		for _, item := range ok {
			fmt.Fprintf(out, "  %s\n", item.Env)
		}
	}
	return nil
}
