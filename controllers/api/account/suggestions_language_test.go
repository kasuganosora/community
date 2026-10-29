package account

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSuggestionsRankSameLanguageFirst(t *testing.T) {
	conn, a := suggestionFixture(t)
	now := time.Now()
	texts := map[uint64]string{
		6: "The weather is really nice today and I went for a walk in the park",
		7: "今天天气很好，我们去公园散步吧，晚上一起吃饭",
		8: "今日はとても良い天気ですね、公園で散歩しました",
		9: "Сегодня очень хорошая погода, пойдём гулять в парк",
	}
	// Higher ids win score ties, so without language ranking 9,8,7,6 then 2.
	for id := uint64(6); id <= 9; id++ {
		u := &models.User{UserName: "u" + string(rune('a'+id)), EmailHash: "h" + string(rune('a'+id)), Status: models.UserStatusNormal}
		require.NoError(t, conn.Create(u).Error)
		require.NoError(t, conn.Create(&models.Account{Id: id, UserId: u.Id, Username: u.UserName, Discoverable: true, LastStatusAt: &now, Avatar: "https://cdn.example/media/" + u.UserName + "/avatar.png"}).Error)
		for i := 0; i < 3; i++ {
			s := &models.Status{AccountId: id, Content: models.StatusContent{Content: texts[id]}, CreatedAt: &now, UpdatedAt: &now}
			s.FillLanguage()
			require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Content").Create(s).Error)
		}
	}
	ids := func(rows []discovery.AccountSuggestion) []uint64 {
		out := []uint64{}
		for _, r := range rows {
			out = append(out, r.Account.Id)
		}
		return out
	}

	// No language signal: unchanged ordering.
	rows, err := discovery.SuggestAccounts(t.Context(), conn, a[0], 20)
	require.NoError(t, err)
	require.Equal(t, []uint64{9, 8, 7, 6, 2}, ids(rows))

	// Accept-Language fallback: English first.
	rows, err = discovery.SuggestAccounts(t.Context(), conn, a[0], 20, discovery.SuggestOptions{AcceptLanguage: "en-US,en;q=0.9"})
	require.NoError(t, err)
	require.Equal(t, uint64(6), ids(rows)[0])

	// UI locale zh-CN: zh exact, then ja (CJK group), then the rest;
	// Accept-Language is ignored once a stronger signal exists.
	_, err = models.UpdateUserSettingData(conn, a[0].UserId, map[string]any{"interface_locale": "zh-CN"})
	require.NoError(t, err)
	rows, err = discovery.SuggestAccounts(t.Context(), conn, a[0], 20, discovery.SuggestOptions{AcceptLanguage: "en"})
	require.NoError(t, err)
	require.Equal(t, []uint64{7, 8, 9, 6, 2}, ids(rows))

	// The V2 endpoint passes Accept-Language through.
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v2/suggestions", nil)
	c.Request.Header.Set("Accept-Language", "ru")
	c.Set("account", a[0])
	ListSuggestions(c)
	require.Equal(t, 200, w.Code)
}
