package serializer

import "github.com/kasuganosora/community/models"

type Application struct {
	Name    string  `json:"name"`
	Website *string `json:"website"`
}

func ToApplication(app *models.Application) *Application {
	if app == nil {
		return &Application{
			Name: "web",
		}
	}

	return &Application{
		Name:    app.Name,
		Website: &app.Website,
	}
}
