package commands

import (
	"fmt"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/migrate/scripts"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

// TagsCommand implements tootctl tags.
func TagsCommand() *cli.Command {
	return &cli.Command{
		Name:  "tags",
		Usage: "Manage hashtags",
		Subcommands: []*cli.Command{
			{
				Name: "clean-invalid",
				Usage: "Find tag rows whose names are not valid hashtags (e.g. \"<span>horror</span></a></p>\"); " +
					"dry run by default, --apply renames/merges recoverable ones and deletes the rest",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "apply", Usage: "actually write the changes (default: dry run)"},
					&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "print every affected tag"},
				},
				Action: func(c *cli.Context) error {
					return cleanInvalidTags(c, db.GetDB().WithContext(c.Context))
				},
			},
		},
	}
}

func cleanInvalidTags(c *cli.Context, conn *gorm.DB) error {
	out := c.App.Writer
	plan, err := scripts.PlanInvalidTags(conn)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	var links int64
	for i, a := range plan {
		counts[a.Action]++
		links += a.Links
		if c.Bool("verbose") || i < 50 {
			switch a.Action {
			case "merge":
				_, _ = fmt.Fprintf(out, "tag %d %q: merge into %q (id %d), %d status link(s)\n", a.Id, a.Name, a.Target, a.TargetId, a.Links)
			case "rename":
				_, _ = fmt.Fprintf(out, "tag %d %q: rename to %q, %d status link(s)\n", a.Id, a.Name, a.Target, a.Links)
			default:
				_, _ = fmt.Fprintf(out, "tag %d %q: delete, %d status link(s)\n", a.Id, a.Name, a.Links)
			}
		}
	}
	if !c.Bool("verbose") && len(plan) > 50 {
		_, _ = fmt.Fprintf(out, "... %d more (use -v to list all)\n", len(plan)-50)
	}
	prefix := "[dry run] "
	if c.Bool("apply") {
		prefix = ""
	}
	_, _ = fmt.Fprintf(out, "%sinvalid tags: %d (rename %d, merge %d, delete %d), status links affected: %d\n",
		prefix, len(plan), counts["rename"], counts["merge"], counts["delete"], links)
	if !c.Bool("apply") || len(plan) == 0 {
		if len(plan) > 0 {
			_, _ = fmt.Fprintln(out, "re-run with --apply to write these changes")
		}
		return nil
	}
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return scripts.CleanInvalidTagsScript{}.Up(tx)
	}); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "done")
	return nil
}
