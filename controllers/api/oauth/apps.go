package oauth

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

func bindCreateApp(c *gin.Context) (CreateAppRequest, error) {
	var req CreateAppRequest

	ct := c.ContentType()
	if strings.Contains(ct, "json") {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
		if err != nil {
			return req, err
		}

		if !json.Valid(body) {
			return req, errors.New("invalid json")
		}

		var raw map[string]json.RawMessage
		if err = json.Unmarshal(body, &raw); err != nil {
			return req, err
		}

		if err = json.Unmarshal(rawField(raw, "client_name"), &req.ClientName); err != nil && len(rawField(raw, "client_name")) > 0 {
			return req, errors.New("invalid client_name")
		}

		if err = json.Unmarshal(rawField(raw, "website"), &req.Website); err != nil && len(rawField(raw, "website")) > 0 {
			return req, errors.New("invalid website")
		}

		if err = json.Unmarshal(rawField(raw, "scopes"), &req.Scopes); err != nil && len(rawField(raw, "scopes")) > 0 {
			return req, errors.New("invalid scopes")
		}

		uris, err := decodeRedirects(rawField(raw, "redirect_uris"))
		if err != nil {
			return req, err
		}

		req.RedirectUris = strings.Join(uris, "\n")
	} else if err := c.ShouldBind(&req); err != nil {
		return req, err
	}

	req.ClientName = strings.TrimSpace(req.ClientName)
	req.Website = strings.TrimSpace(req.Website)
	req.Scopes = strings.TrimSpace(req.Scopes)

	if req.ClientName == "" || req.RedirectUris == "" {
		return req, errors.New("client_name and redirect_uris are required")
	}

	if len(req.ClientName) > 128 {
		return req, errors.New("client_name is too long")
	}

	uris := splitRedirects(req.RedirectUris)
	if len(uris) == 0 {
		return req, errors.New("redirect_uris is required")
	}

	for _, uri := range uris {
		if !isAllowedRedirect(uri) {
			return req, errors.New("invalid redirect_uri")
		}
	}

	req.RedirectUris = strings.Join(uris, "\n")
	if req.Scopes == "" {
		req.Scopes = "read"
	}

	return req, nil
}

func rawField(raw map[string]json.RawMessage, key string) json.RawMessage {
	if raw == nil {
		return nil
	}

	return raw[key]
}

func decodeRedirects(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("redirect_uris is required")
	}

	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return splitRedirects(one), nil
	}

	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return splitRedirects(strings.Join(many, "\n")), nil
	}

	return nil, errors.New("invalid redirect_uris")
}
