package homeserver

import (
	xhtml "golang.org/x/net/html"
	"html"
	"path"
	"strings"
	"unicode"

	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
)

// bridgeStatusContent never sends an unsanitized remote fragment to clients.
// Clients without HTML support receive only the warning for spoiler content.
func bridgeStatusContent(status *models.Status, actorName string) map[string]any {
	formatted := htmlsafe.Sanitize(status.ContentHTML(htmlsafe.RenderOptions{}))
	body := htmlsafe.PlainText(formatted)
	if strings.TrimSpace(body) == "" {
		body = "Attachment"
	}
	warning := status.SpoilerTextPlain()
	metadata := map[string]any{"account": actorName, "transport": "activitypub", "encrypted": false, "sensitive": status.Sensitive}
	if warning != "" || status.Sensitive {
		if warning == "" {
			warning = "Sensitive content"
		}
		metadata["spoiler_text"] = warning
		body = "Content warning: " + warning + " (open the spoiler in a compatible client)"
		// The payload has already been sanitized; only this trusted wrapper may
		// introduce Matrix's spoiler attribute. User HTML cannot inject attributes.
		formatted = "<p>Content warning: " + html.EscapeString(warning) + "</p><span data-mx-spoiler=\"" + html.EscapeString(warning) + "\">" + formatted + "</span>"
	}
	return map[string]any{"msgtype": "m.text", "body": body, "format": "org.matrix.custom.html", "formatted_body": formatted, "org.community.activitypub": metadata}
}

func safeBridgeFilename(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u202e' || r == '\u202d' || r == '\u2066' || r == '\u2067' || r == '\u2068' || r == '\u2069' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		return "attachment"
	}
	if r := []rune(name); len(r) > 255 {
		name = string(r[:255])
	}
	return name
}

func bridgeAttachmentFilename(a models.MediaAttachment) string {
	name := a.Description
	if strings.TrimSpace(name) == "" {
		name = a.FileName
	}
	return safeBridgeFilename(name)
}

func bridgeAttachmentContent(server string, a models.MediaAttachment, media MatrixMedia, sensitive bool) map[string]any {
	kind := "m.file"
	switch a.Type {
	case models.MediaTypeImage:
		kind = "m.image"
	case models.MediaTypeVideo:
		kind = "m.video"
	case models.MediaTypeAudio:
		kind = "m.audio"
	}
	name := bridgeAttachmentFilename(a)
	info := map[string]any{}
	if media.ContentType != "" {
		info["mimetype"] = media.ContentType
	}
	if media.FileSize > 0 {
		info["size"] = media.FileSize
	}
	if media.Width != nil && *media.Width > 0 {
		info["w"] = *media.Width
	}
	if media.Height != nil && *media.Height > 0 {
		info["h"] = *media.Height
	}
	body := name
	if sensitive {
		kind = "m.file"
		body = "敏感附件：" + name
		delete(info, "w")
		delete(info, "h")
	}
	return map[string]any{"msgtype": kind, "body": body, "filename": name, "url": "mxc://" + server + "/" + media.MediaID, "info": info, "org.community.activitypub": map[string]any{"sensitive": sensitive}}
}

// bridgeOutboundText only accepts Matrix's standard HTML envelope. Reply
// fallbacks are removed only after the caller has authorized the reply target.
func bridgeOutboundText(content map[string]any, authenticatedReply bool) (string, string, error) {
	body, _ := content["body"].(string)
	if len(body) > 65536 {
		return "", "", errDirectMessageSkipped
	}
	if content["format"] == "org.matrix.custom.html" {
		formatted, _ := content["formatted_body"].(string)
		if len(formatted) > 65536 {
			return "", "", errDirectMessageSkipped
		}
		if formatted != "" {
			root, err := xhtml.Parse(strings.NewReader(formatted))
			if err != nil {
				return "", "", errDirectMessageSkipped
			}
			var prune func(*xhtml.Node)
			prune = func(n *xhtml.Node) {
				for c := n.FirstChild; c != nil; {
					next := c.NextSibling
					if c.Type == xhtml.ElementNode && c.Data == "mx-reply" && authenticatedReply {
						n.RemoveChild(c)
					} else {
						prune(c)
					}
					c = next
				}
			}
			prune(root)
			var b strings.Builder
			if err = xhtml.Render(&b, root); err != nil {
				return "", "", err
			}
			clean := htmlsafe.Sanitize(b.String())
			if strings.TrimSpace(htmlsafe.StripTags(clean)) != "" {
				return clean, "html", nil
			}
		}
	}
	if authenticatedReply && strings.HasPrefix(body, "> ") {
		if _, tail, ok := strings.Cut(body, "\n\n"); ok {
			body = tail
		}
	}
	if strings.TrimSpace(body) == "" {
		return "", "", errDirectMessageSkipped
	}
	return body, "plain", nil
}
