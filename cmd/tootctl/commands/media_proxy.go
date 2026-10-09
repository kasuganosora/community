package commands

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/urfave/cli/v2"
)

// MediaProxyCommand implements tootctl media-proxy.
func MediaProxyCommand() *cli.Command {
	return &cli.Command{
		Name:  "media-proxy",
		Usage: "Inspect the remote media proxy",
		Subcommands: []*cli.Command{
			{
				Name:  "token",
				Usage: "Print a proxy URL for a remote media URL (for testing)",
				Description: "Mints a token with the server's current key. A negative --ttl gives an already expired link,\n" +
					"which the endpoint must answer with 410; --ttl 0 mints a link that never expires.",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "url", Required: true, Usage: "remote http(s) URL"},
					&cli.StringFlag{Name: "kind", Value: mediaproxy.KindMedia, Usage: "avatar, header, media, emoji or preview"},
					&cli.IntFlag{Name: "ttl", Value: 3600, Usage: "seconds the link stays valid (negative: expired, 0: no expiry)"},
				},
				Action: func(c *cli.Context) error {
					return printProxyToken(c.String("url"), c.String("kind"), c.Int("ttl"))
				},
			},
		},
	}
}

func printProxyToken(rawURL, kind string, ttl int) error {
	if !mediaproxy.ValidKind(kind) {
		return fmt.Errorf("unknown kind %q", kind)
	}
	var expiry int64
	if ttl != 0 {
		expiry = mediaproxy.Now().Add(time.Duration(ttl) * time.Second).Unix()
	}
	token, ok := mediaproxy.Seal(kind, rawURL, expiry)
	if !ok {
		return errors.New("cannot make a token for this URL")
	}
	fmt.Println(strings.TrimRight(systemsettings.GetBaseURL(), "/") + mediaproxy.PathPrefix + token)

	return nil
}
