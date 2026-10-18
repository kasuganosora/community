package datatransfer

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/kasuganosora/community/twofactor"
)

// credentialTwoFactor is the supported two-factor state of a credential.
// Secret and BackupCodes are plaintext from the source: they must never be
// logged, written to a report or included in an error message.
type credentialTwoFactor struct {
	TOTP         bool
	Secret       string
	BackupCodes  []string
	SecurityKeys bool
}

// String keeps fmt/%v from ever printing the secret.
func (credentialTwoFactor) String() string { return "credentialTwoFactor{redacted}" }

// GoString covers %#v.
func (credentialTwoFactor) GoString() string { return "credentialTwoFactor{redacted}" }

// misskeyAuthKeys are the user_profile fields export_misskey.py places in
// Credential.Extensions.
var misskeyAuthKeys = map[string]bool{
	"twoFactorEnabled": true, "twoFactorSecret": true, "twoFactorBackupSecret": true,
	"usePasswordLessLogin": true, "securityKeysAvailable": true,
}

// Reason codes for credentials that still cannot be imported. They name the
// cause only; no source value is ever included.
const (
	authReasonNoAdapter         = "two_factor_without_adapter"
	authReasonUnknownExtension  = "unknown_credential_extension"
	authReasonInvalidSecret     = "invalid_totp_secret"
	authReasonInvalidBackup     = "invalid_backup_codes"
	authReasonPasswordlessNoTOP = "passwordless_without_totp"
)

var authReasonText = map[string]string{
	authReasonNoAdapter:         "two-factor state without importable secrets (e.g. Mastodon needs its Rails keys); reset 2FA on the source or migrate without it",
	authReasonUnknownExtension:  "credential carries extension fields this importer does not understand",
	authReasonInvalidSecret:     "two-factor is enabled but the TOTP secret is missing or not valid base32",
	authReasonInvalidBackup:     "two-factor backup codes are not a list of strings",
	authReasonPasswordlessNoTOP: "passwordless (security-key-only) login without TOTP: Misskey keys are not guaranteed to be discoverable passkeys, so the account would fall back to password-only",
}

// parseCredentialTwoFactor maps Credential.Extensions. It returns (nil, "")
// when the credential has no two-factor state, the state on success, or a
// reason code when the credential must stay unsupported_auth.
func parseCredentialTwoFactor(c Credential) (*credentialTwoFactor, string) {
	ext := strings.TrimSpace(string(c.Extensions))
	if ext == "" || ext == "null" || ext == "{}" {
		if c.TwoFactor {
			return nil, authReasonNoAdapter
		}
		return nil, ""
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(c.Extensions, &fields) != nil {
		return nil, authReasonUnknownExtension
	}
	for k := range fields {
		if !misskeyAuthKeys[k] {
			return nil, authReasonUnknownExtension
		}
	}
	var (
		tf                          credentialTwoFactor
		enabled, passwordless, keys bool
	)
	_ = json.Unmarshal(fields["twoFactorEnabled"], &enabled)
	_ = json.Unmarshal(fields["usePasswordLessLogin"], &passwordless)
	_ = json.Unmarshal(fields["securityKeysAvailable"], &keys)
	tf.SecurityKeys = keys
	if enabled {
		var secret string
		if json.Unmarshal(fields["twoFactorSecret"], &secret) != nil {
			return nil, authReasonInvalidSecret
		}
		norm, err := twofactor.NormalizeSecret(secret)
		if err != nil || len(norm) < 16 {
			return nil, authReasonInvalidSecret
		}
		tf.TOTP, tf.Secret = true, norm
		if raw, ok := fields["twoFactorBackupSecret"]; ok && string(raw) != "null" {
			if json.Unmarshal(raw, &tf.BackupCodes) != nil {
				return nil, authReasonInvalidBackup
			}
		}
	} else if passwordless {
		return nil, authReasonPasswordlessNoTOP
	}
	if !tf.TOTP && !tf.SecurityKeys {
		return nil, ""
	}
	return &tf, ""
}

// redactTwoFactor removes plaintext secrets from a credential extension so
// the value can be digested, preserved or reported safely.
func redactTwoFactor(c Credential) Credential {
	ext := strings.TrimSpace(string(c.Extensions))
	if ext == "" || ext == "null" || ext == "{}" {
		return c
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(c.Extensions, &fields) != nil {
		c.Extensions = json.RawMessage(`{"redacted":true}`)
		return c
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]any{}
	for _, k := range keys {
		switch k {
		case "twoFactorSecret", "twoFactorBackupSecret", "twoFactorTempSecret":
			out[k] = "[redacted]"
		default:
			out[k] = fields[k]
		}
	}
	b, _ := json.Marshal(out)
	c.Extensions = b
	return c
}

// secretFieldNames are source fields holding plaintext second-factor
// material. They are stripped from preserved raw records.
var secretFieldNames = map[string]bool{"twoFactorSecret": true, "twoFactorBackupSecret": true, "twoFactorTempSecret": true}

// RedactSecretFields replaces top-level second-factor secret fields of a JSON
// object with "[redacted]". It reports whether anything changed.
func RedactSecretFields(data json.RawMessage) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(string(data))
	if !strings.HasPrefix(trimmed, "{") || !strings.Contains(trimmed, "twoFactor") {
		return data, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return data, false
	}
	changed := false
	for k, v := range fields {
		if secretFieldNames[k] && string(v) != "null" && string(v) != `"[redacted]"` {
			fields[k] = json.RawMessage(`"[redacted]"`)
			changed = true
		}
	}
	if !changed {
		return data, false
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return json.RawMessage(`{"redacted":true}`), true
	}
	return b, true
}
