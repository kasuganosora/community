package commands

import (
	"errors"
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/kasuganosora/community/controllers/api/oauth"
	"github.com/kasuganosora/community/db"
	"github.com/urfave/cli/v2"
)

// OAuthCommand implements tootctl oauth.
func OAuthCommand() *cli.Command {
	return &cli.Command{
		Name:  "oauth",
		Usage: "Manage OAuth applications",
		Subcommands: []*cli.Command{{
			Name:  "apps",
			Usage: "List or remove registered OAuth applications",
			Subcommands: []*cli.Command{
				{
					Name:  "list",
					Usage: "List applications with their number of active tokens",
					Action: func(c *cli.Context) error {
						apps, err := oauth.ListApplications(db.GetDB())
						if err != nil {
							return err
						}
						w := tabwriter.NewWriter(c.App.Writer, 0, 4, 2, ' ', 0)
						fmt.Fprintln(w, "ID\tNAME\tCLIENT_ID\tACTIVE_TOKENS\tWEBSITE")
						for _, a := range apps {
							fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\n", a.Id, a.Name, a.ClientId, a.Tokens, a.Website)
						}

						return w.Flush()
					},
				},
				{
					Name:        "remove",
					Usage:       "Delete an application and all its tokens, authorization codes and grants",
					ArgsUsage:   "ID",
					Description: "Statuses posted through the application keep their \"via\" reference. Cannot be undone.",
					Action: func(c *cli.Context) error {
						id, err := strconv.ParseUint(c.Args().First(), 10, 64)
						if err != nil || id == 0 || c.NArg() != 1 {
							return errors.New("usage: tootctl oauth apps remove ID")
						}
						tokens, err := oauth.DeleteApplication(db.GetDB(), id)
						if err != nil {
							return err
						}
						fmt.Fprintf(c.App.Writer, "Removed application %d and %d tokens\n", id, tokens)

						return nil
					},
				},
			},
		}},
	}
}
