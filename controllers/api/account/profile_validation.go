package account

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kasuganosora/community/models"
	"golang.org/x/text/language"
)

func validateProfileUpdate(req *UpdateCredentialsRequest) error {
	for _, field := range []struct {
		name  string
		value *string
		limit int
	}{
		{"location", req.Location, 128}, {"followed_message", req.FollowedMessage, 200},
	} {
		if field.value != nil && utf8.RuneCountInString(*field.value) > field.limit {
			return fmt.Errorf("%s exceeds %d characters", field.name, field.limit)
		}
	}
	if req.Birthday != nil && *req.Birthday != "" {
		date, err := time.Parse("2006-01-02", *req.Birthday)
		if err != nil || len(*req.Birthday) != 10 || date.Year() < 1 || date.After(time.Now().Add(24*time.Hour)) {
			return fmt.Errorf("birthday must be a valid past date (YYYY-MM-DD)")
		}
	}
	if req.Language != nil && *req.Language != "" {
		tag, err := language.Parse(*req.Language)
		if err != nil || len(*req.Language) > 35 || strings.TrimSpace(*req.Language) != *req.Language {
			return fmt.Errorf("language must be a valid language tag")
		}
		*req.Language = tag.String()
	}
	if req.ReactionAcceptance != nil && !slices.Contains(models.ReactionAcceptances, *req.ReactionAcceptance) {
		return fmt.Errorf("invalid reaction acceptance")
	}
	if len(req.FieldsAttributes) > models.MaxProfileFields {
		return fmt.Errorf("at most %d profile fields are allowed", models.MaxProfileFields)
	}
	for _, field := range req.FieldsAttributes {
		if utf8.RuneCountInString(field.Name) > 255 || utf8.RuneCountInString(field.Value) > 2048 {
			return fmt.Errorf("profile field name or value is too long")
		}
	}
	return nil
}
