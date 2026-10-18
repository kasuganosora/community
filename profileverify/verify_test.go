package profileverify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(r *http.Request, text string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(text)), Header: http.Header{"Content-Type": []string{"text/html"}}, Request: r}
}

func TestBacklinkRequiresAnExactProfileAndRelMe(t *testing.T) {
	identity := "https://local.example/@alice"
	for _, test := range []struct {
		html  string
		valid bool
	}{
		{`<a href="https://local.example/@alice" rel="noopener me">Me</a>`, true},
		{`<link href="https://local.example/@alice#profile" rel="me"/>`, true},
		{`<a href="https://local.example/@mallory" rel="me">Me</a>`, false},
		{`<a href="https://local.example/@alice">Alice</a>`, false},
		{`<a href="https://local.example/@alice.evil" rel="me">Alice</a>`, false},
		{`<script>document.write('<a rel="me" href="https://local.example/@alice">')</script>`, false},
	} {
		client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) { return response(r, test.html), nil })}
		ok, err := verifyURL(context.Background(), client, "https://blog.example", []string{identity})
		require.NoError(t, err)
		require.Equal(t, test.valid, ok, test.html)
	}
}

func TestProfileProofIsDurableAndOldWorkCannotVerifyAReplacement(t *testing.T) {
	db, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	raw, err := db.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	require.NoError(t, db.AutoMigrate(&models.Account{}, &models.AccountExtra{}, &models.ProfileFieldVerification{}))
	a := models.Account{Id: 8, Username: "alice"}
	require.NoError(t, db.Create(&a).Error)
	setFields := func(value string) {
		extra := models.AccountExtra{AccountId: a.Id, Fields: []models.AccountField{{Name: "Blog", Val: value}}}
		require.NoError(t, db.Model(&extra).Select("Fields").Updates(&extra).Error)
		require.NoError(t, Enqueue(db, a.Id))
	}
	previousBase, previousConfigured := config.C.AppBaseURL, config.AppBaseURLConfigured()
	config.SetAppBaseURL("https://local.example", true)
	t.Cleanup(func() { config.SetAppBaseURL(previousBase, previousConfigured) })
	setFields("https://old.example")
	calls := 0
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			setFields("https://replacement.example")
		}
		return response(r, `<a rel="me" href="https://local.example/@alice">Me</a>`), nil
	})}
	require.NoError(t, process(context.Background(), db, a.Id, client))
	var extra models.AccountExtra
	require.NoError(t, db.First(&extra, "account_id = ?", a.Id).Error)
	require.Equal(t, "https://replacement.example", extra.Fields[0].Val)
	require.Nil(t, extra.Fields[0].VerifiedAt)
	require.NoError(t, process(context.Background(), db, a.Id, client))
	require.NoError(t, db.First(&extra, "account_id = ?", a.Id).Error)
	require.NotNil(t, extra.Fields[0].VerifiedAt)
	var count int64
	require.NoError(t, db.Model(&models.ProfileFieldVerification{}).Count(&count).Error)
	require.Zero(t, count)

	setFields("https://offline.example")
	client.Transport = transport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	require.NoError(t, process(context.Background(), db, a.Id, client))
	var job models.ProfileFieldVerification
	require.NoError(t, db.First(&job, "account_id = ?", a.Id).Error)
	require.Equal(t, "pending", job.State)
	require.Equal(t, 1, job.Attempts)
	require.True(t, job.NextAttemptAt.After(time.Now()))
}
