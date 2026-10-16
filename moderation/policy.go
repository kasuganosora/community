package moderation

import (
	"errors"

	"github.com/kasuganosora/community/models"
)

// ErrProhibitedWords is returned when a note contains a prohibited word
// (Misskey error 689ee33f "Note contains prohibited words").
var ErrProhibitedWords = errors.New("Note contains prohibited words")

// ErrProhibitedName is returned for a username or display name containing a
// word from prohibitedWordsForNameOfUser.
var ErrProhibitedName = errors.New("Name contains prohibited words")

// ErrPreservedUsername is returned for a reserved username.
var ErrPreservedUsername = errors.New("This username is reserved")

// Note is the text of a post as seen by the keyword checks.
type Note struct {
	CW          string // spoiler / content warning
	Text        string // plain text body (HTML already stripped for remote notes)
	PollChoices []string
	Host        string // author host, "" for local
}

// ApplyNotePolicy follows Misskey's NoteCreateService.create:
//
//  1. a public note whose cw (or text when there is no cw) contains a
//     sensitive word becomes "home" (our unlisted);
//  2. a note whose cw+text+poll choices contain a prohibited word is
//     rejected with ErrProhibitedWords, for local and remote authors;
//  3. a public note from a silenced remote host becomes "home".
//
// It returns the visibility to store.
func (s *Snapshot) ApplyNotePolicy(n Note, visibility uint8) (uint8, error) {
	if visibility == models.StatusVisibilityPublic {
		probe := n.CW
		if probe == "" {
			probe = n.Text
		}
		if s.IsSensitiveText(probe) {
			visibility = models.StatusVisibilityUnlisted
		}
	}
	if !s.prohibited.Empty() && s.HasProhibitedWords(NoteTextForKeywordCheck(n.CW, n.Text, n.PollChoices, nil)) {
		return visibility, ErrProhibitedWords
	}
	if visibility == models.StatusVisibilityPublic && n.Host != "" && s.IsSilencedHost(n.Host) {
		visibility = models.StatusVisibilityUnlisted
	}
	return visibility, nil
}

// CheckLocalNames validates a new or changed local username / display name
// (Misskey SignupService and i/update). Pass "" for a value that is not
// being set. preserved is checked only for new usernames.
func (s *Snapshot) CheckLocalNames(username, displayName string, checkPreserved bool) error {
	if username != "" {
		if checkPreserved && s.IsPreservedUsername(username) {
			return ErrPreservedUsername
		}
		if s.IsProhibitedName(username) {
			return ErrProhibitedName
		}
	}
	if displayName != "" && s.IsProhibitedName(displayName) {
		return ErrProhibitedName
	}
	return nil
}
