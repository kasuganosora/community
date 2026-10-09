package serializer

import "github.com/kasuganosora/community/models"

// Application is the minimal Mastodon Application entity embedded in
// statuses. website is null or a valid absolute URL, never "".
type Application struct {
	Name    string  `json:"name"`
	Website *string `json:"website"`
}

func ToApplication(app *models.Application) *Application {
	if app == nil {
		return &Application{Name: "web"}
	}

	out := &Application{Name: app.Name}
	if isAbsoluteURL(app.Website) {
		w := app.Website
		out.Website = &w
	}

	return out
}
