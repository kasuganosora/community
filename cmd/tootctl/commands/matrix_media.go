package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/matrix/homeserver"
	"github.com/urfave/cli/v2"
)

func matrixMediaGCCommand() *cli.Command {
	return &cli.Command{
		Name: "media-gc", Usage: "Collect unreferenced managed Matrix media (dry run by default)",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "confirm", Usage: "apply collection; otherwise only inspect"},
			&cli.StringFlag{Name: "origin", Usage: "restrict to local or remote origin"},
			&cli.IntFlag{Name: "limit", Value: 32, Usage: "maximum records inspected (1–1000)"},
			&cli.DurationFlag{Name: "grace", Value: 7 * 24 * time.Hour, Usage: "unreferenced grace period (minimum 24h)"},
		},
		Action: func(c *cli.Context) error {
			ctx, cancel := context.WithTimeout(c.Context, time.Minute)
			defer cancel()
			result, err := homeserver.CollectMedia(ctx, db.GetDB(), time.Now(), homeserver.MediaGCOptions{Apply: c.Bool("confirm"), Origin: c.String("origin"), Limit: c.Int("limit"), Grace: c.Duration("grace")})
			fmt.Fprintf(c.App.Writer, "dry_run=%t scanned=%d retained=%d waiting=%d deletions=%d\n", !c.Bool("confirm"), result.Scanned, result.Retained, result.Waiting, result.Deleted)
			return err
		},
	}
}
