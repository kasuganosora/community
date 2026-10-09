package activitypub

import (
	"bytes"
	"encoding/json"
)

// Keep the ordinary single-key actor representation unchanged while retaining
// source instances which published several simultaneously active RSA key IDs.
func (a Actor) MarshalJSON() ([]byte, error) {
	type plain Actor
	var keys any
	if a.PublicKey != nil {
		keys = a.PublicKey
	}
	if len(a.PublicKeys) > 0 {
		all := make([]*PublicKey, 0, len(a.PublicKeys)+1)
		if a.PublicKey != nil {
			all = append(all, a.PublicKey)
		}
		all = append(all, a.PublicKeys...)
		keys = all
	}
	return json.Marshal(struct {
		*plain
		PublicKey any `json:"publicKey,omitempty"`
	}{plain: (*plain)(&a), PublicKey: keys})
}

// UnmarshalJSON accepts the actor shapes seen across the fediverse. Besides
// multiple public keys, implementations disagree on several JSON-LD fields:
// Mastodon publishes alsoKnownAs as an array, some servers publish url as a
// Link object or an array, and icon/image may be an array of Image objects or
// a bare URL. A strict decoder rejected such actors outright, which made
// remote accounts impossible to resolve.
func (a *Actor) UnmarshalJSON(data []byte) error {
	type plain Actor
	var raw struct {
		*plain
		PublicKey   json.RawMessage `json:"publicKey"`
		URL         json.RawMessage `json:"url"`
		AlsoKnownAs json.RawMessage `json:"alsoKnownAs"`
		MovedTo     json.RawMessage `json:"movedTo"`
		Icon        json.RawMessage `json:"icon"`
		Image       json.RawMessage `json:"image"`
		Name        json.RawMessage `json:"name"`
		Summary     json.RawMessage `json:"summary"`
	}
	*a = Actor{}
	raw.plain = (*plain)(a)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	a.URL = firstLinkHref(raw.URL)
	a.AlsoKnownAs = firstLinkHref(raw.AlsoKnownAs)
	a.MovedTo = firstLinkHref(raw.MovedTo)
	a.Name = jsonText(raw.Name)
	a.Summary = jsonText(raw.Summary)
	a.Icon = firstImage(raw.Icon)
	a.Image = firstImage(raw.Image)

	value := bytes.TrimSpace(raw.PublicKey)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return nil
	}
	if value[0] == '[' {
		var keys []*PublicKey
		if err := json.Unmarshal(value, &keys); err != nil {
			return err
		}
		if len(keys) > 0 {
			a.PublicKey = keys[0]
			a.PublicKeys = keys[1:]
		}
		return nil
	}
	return json.Unmarshal(value, &a.PublicKey)
}

// jsonText returns a JSON string value, or "" for null and non-string values.
func jsonText(value json.RawMessage) string {
	var s string
	if json.Unmarshal(value, &s) == nil {
		return s
	}
	return ""
}

// firstLinkHref extracts a URL from a string, a Link/Object ({"href"} or
// {"id"}/{"url"}) or an array of either, preferring text/html links.
func firstLinkHref(value json.RawMessage) string {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return ""
	}
	switch value[0] {
	case '"':
		return jsonText(value)
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(value, &items) != nil {
			return ""
		}
		first := ""
		for _, item := range items {
			href := firstLinkHref(item)
			if href == "" {
				continue
			}
			var link struct {
				MediaType string `json:"mediaType"`
			}
			if json.Unmarshal(item, &link) == nil && link.MediaType == "text/html" {
				return href
			}
			if first == "" {
				first = href
			}
		}
		return first
	case '{':
		var link struct {
			Href string          `json:"href"`
			ID   string          `json:"id"`
			URL  json.RawMessage `json:"url"`
		}
		if json.Unmarshal(value, &link) != nil {
			return ""
		}
		if link.Href != "" {
			return link.Href
		}
		if href := firstLinkHref(link.URL); href != "" {
			return href
		}
		return link.ID
	}
	return ""
}

// firstImage decodes icon/image values given as an Image object, an array of
// Image objects or a bare URL. Unknown shapes are ignored instead of failing
// the whole actor.
func firstImage(value json.RawMessage) *Image {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return nil
	}
	switch value[0] {
	case '"':
		if u := jsonText(value); u != "" {
			return &Image{Type: "Image", URL: u}
		}
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(value, &items) != nil {
			return nil
		}
		for _, item := range items {
			if img := firstImage(item); img != nil {
				return img
			}
		}
	case '{':
		var obj struct {
			Type      string          `json:"type"`
			MediaType string          `json:"mediaType"`
			URL       json.RawMessage `json:"url"`
			Href      string          `json:"href"`
			Name      json.RawMessage `json:"name"`
		}
		if json.Unmarshal(value, &obj) != nil {
			return nil
		}
		u := firstLinkHref(obj.URL)
		if u == "" {
			u = obj.Href
		}
		if u == "" {
			return nil
		}
		return &Image{Type: obj.Type, MediaType: obj.MediaType, URL: u, Name: jsonText(obj.Name)}
	}
	return nil
}
