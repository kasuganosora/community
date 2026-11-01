package commands

import (
	"fmt"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/mediagc"
	"github.com/kasuganosora/community/storage"
	"github.com/urfave/cli/v2"
)

// MediaPurgeUnattachedCommand implements tootctl media purge-unattached.
func MediaPurgeUnattachedCommand() *cli.Command {
	return &cli.Command{
		Name:  "purge-unattached",
		Usage: "Delete unattached media older than the retention window (dry run unless --confirm)",
		Flags: []cli.Flag{
			&cli.DurationFlag{
				Name:  "min-age",
				Value: mediagc.DefaultMinAge,
				Usage: "Keep unattached media newer than this (default 168h, minimum 1h). Status deletion refreshes this clock so the window is also the redraft grace period.",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Only report what would be deleted (the default; wins over --confirm)",
			},
			&cli.BoolFlag{
				Name:  "confirm",
				Usage: "Delete the expired rows and their storage objects",
			},
		},
		Action: purgeUnattachedMedia,
	}
}

func purgeUnattachedMedia(c *cli.Context) error {
	out := c.App.Writer
	minAge := c.Duration("min-age")
	if minAge < mediagc.MinimumMinAge {
		return fmt.Errorf("--min-age must be at least %s", mediagc.MinimumMinAge)
	}

	deleteRows := c.Bool("confirm") && !c.Bool("dry-run")
	mode := "DRY RUN (pass --confirm to delete)"
	if deleteRows {
		mode = "DELETING expired unattached media"
		if err := storage.Default().Check(c.Context); err != nil {
			return fmt.Errorf("storage backend unavailable: %w", err)
		}
	}

	_, _ = fmt.Fprintf(out, "Unattached media older than %s - %s\n", minAge, mode)
	stats, err := mediagc.PurgeRemaining(c.Context, db.GetDB(), mediagc.Options{
		MinAge: minAge,
		DryRun: !deleteRows,
		Limit:  2000,
		Out:    out,
	})
	_, _ = fmt.Fprintf(out, "Candidates %d, would remove %d, removed %d, shared objects kept %d, profile rows kept %d, failed %d\n",
		stats.Candidates, stats.WouldRemove, stats.Removed, stats.SharedKept, stats.ProfileKept, stats.Failed)
	if stats.StoppedEarly {
		_, _ = fmt.Fprintln(out, "Stopped at the row limit; run again to continue")
	}

	return err
}
