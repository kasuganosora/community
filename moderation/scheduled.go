package moderation

import (
	"github.com/kasuganosora/community/models"
)

func init() { models.ScheduledPublishPolicy = CheckScheduledStatus }

// CheckScheduledStatus re-applies the note policy when a scheduled local
// status is published, so word lists changed after scheduling still apply
// (Misskey checks when the note is actually created).
func CheckScheduledStatus(s *models.Status) error {
	var choices []string
	if s.Poll != nil {
		for _, o := range s.Poll.Options {
			if o != nil {
				choices = append(choices, o.Content)
			}
		}
	}
	v, err := Current().ApplyNotePolicy(Note{CW: s.Content.SpoilerText, Text: s.Content.EditableText(), PollChoices: choices}, s.Visibility)
	if err != nil {
		return err
	}
	s.Visibility = v
	return nil
}
