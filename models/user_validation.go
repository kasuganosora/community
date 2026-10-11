package models

import (
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Rules shared by web registration and the tootctl CLI.
const (
	MaxUsernameLength = 20
	MinPasswordLength = 8
)

// usernameRegexp allows letters (including CJK), digits and underscores.
// Usernames end up in URLs (/users/<name>, /@<name>) and in
// acct:<name>@<domain> WebFinger handles, so characters such as '/', '.',
// '@', '?', '#' or whitespace must never be accepted. Before this check a
// username like "../../etc" could be registered.
var usernameRegexp = regexp.MustCompile(`^[\p{L}\p{N}_]+$`)

var (
	ErrInvalidUsername  = errors.New("username may only contain letters, numbers and underscores (max 20 characters)")
	ErrInvalidEmail     = errors.New("email is invalid")
	ErrPasswordTooShort = errors.New("password is too short (minimum is 8 characters)")
)

// ValidateUsername checks a local username.
func ValidateUsername(username string) error {
	if n := utf8.RuneCountInString(username); n == 0 || n > MaxUsernameLength || !usernameRegexp.MatchString(username) || IsReservedBridgeUsername(username) {
		return ErrInvalidUsername
	}

	return nil
}

// ValidateEmail checks that email is a bare address.
func ValidateEmail(email string) error {
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || !strings.Contains(email, "@") {
		return ErrInvalidEmail
	}

	return nil
}

// ValidatePassword enforces the password policy.
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}

	return nil
}

// IsReservedBridgeUsername keeps synthetic ActivityPub users separate from login accounts.
func IsReservedBridgeUsername(username string) bool {
	return strings.HasPrefix(strings.ToLower(username), "_ap_")
}
