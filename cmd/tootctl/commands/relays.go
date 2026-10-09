package commands

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

// RelaysCommand implements tootctl relays: the same service as the admin
// relay API (/api/v1/admin/relays). Follow/Undo activities are queued on the
// persistent delivery queue; the running server sends them (with retries)
// signed by the instance actor <base>/actor.
func RelaysCommand() *cli.Command {
	return &cli.Command{
		Name:  "relays",
		Usage: "Manage ActivityPub relay subscriptions",
		Subcommands: []*cli.Command{
			{
				Name:      "add",
				Usage:     "Subscribe to a relay: store its inbox and send a Follow from the instance actor",
				ArgsUsage: "INBOX_URL[...]",
				Action:    relaysAdd,
			},
			{
				Name:   "list",
				Usage:  "List relays with their state (idle, pending, accepted, rejected)",
				Action: relaysList,
			},
			{
				Name:      "enable",
				Usage:     "(Re)send the Follow to a relay (e.g. after a rejection or a disable)",
				ArgsUsage: "ID|INBOX_URL",
				Action:    relaysAction("enable"),
			},
			{
				Name:      "disable",
				Usage:     "Unsubscribe (send Undo Follow) but keep the relay",
				ArgsUsage: "ID|INBOX_URL",
				Action:    relaysAction("disable"),
			},
			{
				Name:      "remove",
				Usage:     "Unsubscribe and delete the relay",
				ArgsUsage: "ID|INBOX_URL",
				Action:    relaysAction("remove"),
			},
		},
	}
}

var relayStateNames = map[models.RelayState]string{
	models.RelayStateIdle:     "idle",
	models.RelayStatePending:  "pending",
	models.RelayStateAccepted: "accepted",
	models.RelayStateRejected: "rejected",
}

func relaysAdd(c *cli.Context) error {
	if c.NArg() == 0 {
		return errors.New("usage: tootctl relays add INBOX_URL[...]")
	}
	service := activitypub.NewRelayService()
	failed := 0
	for _, inbox := range c.Args().Slice() {
		relay, err := service.CreateRelay(inbox)
		if err != nil {
			fmt.Fprintf(c.App.Writer, "%s: %v\n", inbox, err)
			failed++
			continue
		}
		if err := service.EnableRelay(relay.ID); err != nil {
			fmt.Fprintf(c.App.Writer, "%s: added (id %d) but the Follow could not be queued: %v\n", inbox, relay.ID, err)
			failed++
			continue
		}
		fmt.Fprintf(c.App.Writer, "%s: added (id %d), Follow queued from %s, state pending\n", inbox, relay.ID, service.GetInstanceActorURI())
	}
	if failed > 0 {
		return fmt.Errorf("%d relay(s) failed", failed)
	}
	return nil
}

func relaysList(c *cli.Context) error {
	relays, err := activitypub.NewRelayService().GetAllRelays()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(c.App.Writer, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATE\tINBOX\tUPDATED")
	for _, r := range relays {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", r.ID, relayStateNames[r.State], r.InboxURL, r.UpdatedAt.Format("2006-01-02 15:04:05"))
	}
	return w.Flush()
}

func findRelay(service *activitypub.RelayService, ref string) (*models.Relay, error) {
	if id, err := strconv.ParseUint(ref, 10, 64); err == nil {
		return service.GetRelayByID(id)
	}
	relays, err := service.GetAllRelays()
	if err != nil {
		return nil, err
	}
	for i := range relays {
		if relays[i].InboxURL == strings.TrimSpace(ref) {
			return &relays[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func relaysAction(action string) cli.ActionFunc {
	return func(c *cli.Context) error {
		if c.NArg() != 1 {
			return fmt.Errorf("usage: tootctl relays %s ID|INBOX_URL", action)
		}
		service := activitypub.NewRelayService()
		relay, err := findRelay(service, c.Args().First())
		if err != nil {
			return fmt.Errorf("relay %q not found", c.Args().First())
		}
		switch action {
		case "enable":
			err = service.EnableRelay(relay.ID)
		case "disable":
			err = service.DisableRelay(relay.ID)
		case "remove":
			err = service.DeleteRelay(relay.ID)
		}
		if err != nil {
			return err
		}
		done := map[string]string{"enable": "Follow queued (pending)", "disable": "Undo Follow queued (idle)", "remove": "removed"}[action]
		fmt.Fprintf(c.App.Writer, "%s: %s\n", relay.InboxURL, done)
		return nil
	}
}
