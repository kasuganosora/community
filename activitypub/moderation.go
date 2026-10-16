package activitypub

import (
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
)

// apPollChoices returns the option names of a Question (oneOf / anyOf).
func apPollChoices(object map[string]any) []string {
	var out []string
	for _, key := range []string{"oneOf", "anyOf"} {
		options, _ := object[key].([]any)
		for _, o := range options {
			if m, ok := o.(map[string]any); ok {
				if name, ok := m["name"].(string); ok {
					out = append(out, name)
				}
			}
		}
	}
	return out
}

// applyInboundNotePolicy applies the instance moderation rules to a remote
// note before it is stored, like Misskey's NoteCreateService.create does for
// notes received by ApNoteService: prohibited words reject the note (false),
// sensitive words or a silenced author host turn "public" into unlisted.
func applyInboundNotePolicy(status *models.Status, object map[string]any, host string) bool {
	visibility, err := moderation.Current().ApplyNotePolicy(moderation.Note{
		CW:          status.Content.SpoilerText,
		Text:        htmlsafe.StripTags(status.Content.Content),
		PollChoices: apPollChoices(object),
		Host:        host,
	}, status.Visibility)
	if err != nil {
		return false
	}
	status.Visibility = visibility
	return true
}

// applyMediaSilence marks the files of a note from a media-silenced host as
// sensitive (Misskey DriveService: isMediaSilencedHost → isSensitive).
func applyMediaSilence(attachments models.Attachments, host string) {
	if len(attachments) == 0 || !moderation.Current().IsMediaSilencedHost(host) {
		return
	}
	for _, a := range attachments {
		if a != nil {
			a.Sensitive = true
		}
	}
}
