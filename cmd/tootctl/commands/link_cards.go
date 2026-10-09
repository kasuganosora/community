package commands

import (
	"fmt"

	"github.com/kasuganosora/community/linkcard"
	"github.com/urfave/cli/v2"
)

// LinkCardsCommand implements tootctl link-cards.
func LinkCardsCommand() *cli.Command {
	return &cli.Command{
		Name:  "link-cards",
		Usage: "Manage link preview cards",
		Subcommands: []*cli.Command{{
			Name:  "backfill",
			Usage: "Create link preview cards for recent statuses that have none",
			Flags: []cli.Flag{
				&cli.IntFlag{Name: "days", Value: 7, Usage: "only statuses from the last N days (0 = any age)"},
				&cli.IntFlag{Name: "limit", Value: 1000, Usage: "maximum number of statuses to process (0 = no limit)"},
				&cli.IntFlag{Name: "concurrency", Aliases: []string{"c"}, Value: 4, Usage: "pages fetched in parallel"},
				&cli.Float64Flag{Name: "rate", Value: 5, Usage: "maximum statuses started per second (0 = unlimited)"},
				&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "print every status"},
			},
			Action: func(c *cli.Context) error {
				svc := linkcard.NewService()
				verbose := c.Bool("verbose")
				done := 0
				stats, err := svc.Backfill(c.Context, c.Int("days"), c.Int("limit"), c.Int("concurrency"), c.Float64("rate"),
					func(id uint64, o linkcard.Outcome, err error) {
						done++
						if verbose || err != nil {
							fmt.Printf("status %d: %s %v\n", id, [...]string{"skipped", "created", "reused", "failed"}[o], errOrEmpty(err))
						} else if done%50 == 0 {
							fmt.Printf("... %d processed\n", done)
						}
					})
				fmt.Printf("Candidates: %d, created: %d, reused: %d, no preview: %d, skipped: %d, errors: %d\n",
					stats.Candidates, stats.Created, stats.Reused, stats.Failed, stats.Skipped, stats.Errors)
				return err
			},
		}},
	}
}

func errOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
