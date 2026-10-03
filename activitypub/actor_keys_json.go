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
func (a *Actor) UnmarshalJSON(data []byte) error {
	type plain Actor
	var raw struct {
		*plain
		PublicKey json.RawMessage `json:"publicKey"`
	}
	*a = Actor{}
	raw.plain = (*plain)(a)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
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
