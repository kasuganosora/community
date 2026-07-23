package recaptcha

import (
	"github.com/kasuganosora/community/httpclient"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/pkg/errors"
	"net/url"
)

// reCAPTCHA v3
// https://developers.google.com/recaptcha/docs/v3
// API https://www.google.com/recaptcha/api/siteverify

func SiteVerify(response string, remoteIp string) (ok bool, err error) {
	secret := systemsettings.GetString(systemsettings.KeyRecaptchaSecretKey, "")
	if secret == "" {
		err = errors.New("recaptcha secret key is empty")

		return ok, err
	}

	client := httpclient.NewClient()
	req := client.NewRequest("GET", "https://www.google.com/recaptcha/api/siteverify")
	postData := make(url.Values)

	postData.Set("secret", secret)
	postData.Set("response", response)

	if remoteIp != "" {
		postData.Set("remoteip", remoteIp)
	}

	req.SetFormUrlencoded(postData)

	resp, err := req.Do()
	if err != nil {
		err = errors.WithStack(err)

		return ok, err
	}

	defer func() { _ = resp.Body.Close() }()

	result, err := resp.JsonMap()
	if err != nil {
		err = errors.WithStack(err)

		return ok, err
	}

	if result["success"] == nil {
		err = errors.New("recaptcha response error")

		return ok, err
	}

	ok = result["success"].(bool) //nolint:forcetypeassert

	return ok, err
}
