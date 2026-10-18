package account

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/stretchr/testify/require"
)

func TestProfilePreferencesRoundTripPreserveAndClear(t *testing.T) {
	conn := setupDirectoryDB(t)
	require.NoError(t, conn.AutoMigrate(&models.ProfileFieldVerification{}, &models.Role{}, &models.RoleUser{}))
	account := &models.Account{Id: 901, Username: "alice"}
	require.NoError(t, conn.Create(account).Error)
	patch := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("PATCH", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("account", account)
		UpdateCredentials(c)
		require.Empty(t, c.Errors)
		return w
	}
	response := patch(`{"location":"上海","birthday":"2000-02-29","language":"zh-cn","is_cat":true,"bot":true,"followed_message":"谢谢关注！","reaction_acceptance":"nonSensitiveOnly","fields_attributes":[{"name":"网站","value":"https://example.org/about"},{"name":"喜欢","value":"<b>猫</b> & coffee"}]}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	var output map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &output))
	require.Equal(t, "zh-CN", output["language"])
	require.Equal(t, "谢谢关注！", output["followed_message"])
	require.True(t, output["is_cat"].(bool))
	var queued models.ProfileFieldVerification
	require.NoError(t, conn.First(&queued, "account_id = ?", account.Id).Error)
	require.Equal(t, uint64(1), queued.Version)
	require.Equal(t, "pending", queued.State)
	public, err := json.Marshal(serializer.ToAccount(account))
	require.NoError(t, err)
	require.NotContains(t, string(public), "followed_message", "private greeting is not exposed on the public account")

	response = patch(`{"display_name":"Alice"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "上海", account.Location)
	require.Equal(t, "2000-02-29", account.Birthday)
	require.Equal(t, "zh-CN", account.Language)
	require.Len(t, account.Extra.Fields, 2)
	require.Equal(t, "<b>猫</b> & coffee", account.Extra.Fields[1].Val)

	verify := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(verify)
	ctx.Request = httptest.NewRequest("GET", "/", nil)
	ctx.Set("account", account)
	VerifyCredentials(ctx)
	require.Equal(t, 200, verify.Code, verify.Body.String())
	var credentials AccountWithSource
	require.NoError(t, json.Unmarshal(verify.Body.Bytes(), &credentials))
	require.Equal(t, "zh-CN", credentials.Source.Language)
	require.Equal(t, "<b>猫</b> & coffee", credentials.Source.Fields[1].Value)
	require.Equal(t, "谢谢关注！", credentials.FollowedMessage)

	response = patch(`{"location":"","birthday":"","language":"","is_cat":false,"bot":false,"followed_message":"","reaction_acceptance":"all","fields_attributes":[]}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Empty(t, account.Location)
	require.Empty(t, account.Birthday)
	require.Empty(t, account.Language)
	require.Empty(t, account.FollowedMessage)
	require.False(t, account.IsCat)
	require.False(t, account.Bot)
	require.Empty(t, account.Extra.Fields)
}

func TestProfileUpdateValidationAndFieldProofCannotBeForged(t *testing.T) {
	for _, body := range []string{
		`{"birthday":"2023-02-29"}`, `{"birthday":"2999-01-01"}`, `{"language":"not a language"}`,
		`{"reaction_acceptance":"anything"}`, `{"location":"` + strings.Repeat("地", 129) + `"}`,
		`{"followed_message":"` + strings.Repeat("喵", 201) + `"}`,
	} {
		var req UpdateCredentialsRequest
		require.NoError(t, json.Unmarshal([]byte(body), &req))
		require.Error(t, validateProfileUpdate(&req), body)
	}
	conn := setupDirectoryDB(t)
	a := &models.Account{Id: 902, Username: "bob"}
	require.NoError(t, conn.Create(a).Error)
	now := time.Now()
	require.NoError(t, conn.Model(&models.AccountExtra{AccountId: a.Id}).Select("Fields").Updates(&models.AccountExtra{Fields: []models.AccountField{{Name: "old", Val: "https://old.example", VerifiedAt: &now}}}).Error)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PATCH", "/", strings.NewReader(`{"fields_attributes":[{"name":"new","value":"https://new.example","verified_at":"2026-01-01T00:00:00Z"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", a)
	UpdateCredentials(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Nil(t, a.Extra.Fields[0].VerifiedAt)
}

func TestFollowGreetingWaitsForApproval(t *testing.T) {
	a := &models.Account{Id: 1, FollowedMessage: "Welcome!"}
	require.Empty(t, serializer.ToFollowStatus(a, nil).FollowedMessage)
	require.Empty(t, serializer.ToFollowStatus(a, &models.Follow{Status: models.FollowStatusRequested}).FollowedMessage)
	require.Equal(t, "Welcome!", serializer.ToFollowStatus(a, &models.Follow{Status: models.FollowStatusFollowed}).FollowedMessage)
}
