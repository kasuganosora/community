package account

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func withBlockedHosts(t *testing.T, hosts []string) {
	t.Helper()
	systemsettings.Set(moderation.KeyBlockedHosts, hosts)
	moderation.Invalidate()
	t.Cleanup(func() {
		systemsettings.Set(moderation.KeyBlockedHosts, []string{})
		moderation.Invalidate()
	})
}

func lookupJSON(t *testing.T, target string, viewer *models.Account) (int, string, map[string]any) {
	t.Helper()
	w := call(t, Lookup, target, viewer)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
	return w.Code, w.Body.String(), body
}

func availabilityOf(body map[string]any) string {
	raw, _ := body["availability"].(map[string]any)
	reason, _ := raw["reason"].(string)
	return reason
}

func domainOf(host string) *string { return &host }

func TestLookupProfileAvailability(t *testing.T) {
	viewer := acctFixture(t)
	conn := database.GetDB()
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.MuteAccount{}))

	suspendedUser := &models.User{UserName: "suspended-login", EmailHash: "suspended-login-hash", Properties: map[string]any{}, IsBan: true}
	require.NoError(t, conn.Create(suspendedUser).Error)
	require.NoError(t, conn.Create(&models.Account{Username: "suspendedlogin", UserId: suspendedUser.Id, DisplayName: "Suspended Login", Note: "login-ban-note"}).Error)

	remoteDomain := "remote.example"
	require.NoError(t, conn.Create(&models.Account{Username: "bannedremote", Domain: &remoteDomain, OriginId: "https://remote.example/users/bannedremote", IsBan: true, DisplayName: "Banned Remote", Note: "remote-ban-note"}).Error)

	blocked := "blocked.example"
	require.NoError(t, conn.Create(&models.Account{Username: "ada", Domain: &blocked, OriginId: "https://blocked.example/users/ada", DisplayName: "Cached Ada", Note: "cached-ada-note"}).Error)
	sub := "a.blocked.example"
	require.NoError(t, conn.Create(&models.Account{Username: "ada", Domain: &sub, OriginId: "https://a.blocked.example/users/ada", DisplayName: "Sub Ada", Note: "sub-ada-note"}).Error)
	notBlocked := "notblocked.example"
	require.NoError(t, conn.Create(&models.Account{Username: "ada", Domain: &notBlocked, OriginId: "https://notblocked.example/users/ada", DisplayName: "Not Blocked", Note: "healthy-note"}).Error)
	clear := "clear.example"
	require.NoError(t, conn.Create(&models.Account{Username: "neo", Domain: &clear, OriginId: "https://clear.example/users/neo", DisplayName: "Neo", Note: "neo-note"}).Error)

	dbDomain := "DB.Example"
	require.NoError(t, conn.Create(&models.DomainBlock{
		Domain:         dbDomain,
		Severity:       "suspend",
		PrivateComment: "synthetic-private-remark",
		PublicComment:  "synthetic-public-remark",
	}).Error)
	child := "user.db.example"
	require.NoError(t, conn.Create(&models.Account{Username: "bea", Domain: &child, OriginId: "https://user.db.example/users/bea", DisplayName: "Bea", Note: "bea-note"}).Error)

	emptySeverity := "empty-severity.example"
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: emptySeverity, Severity: ""}).Error)
	require.NoError(t, conn.Create(&models.Account{Username: "cy", Domain: &emptySeverity, OriginId: "https://empty-severity.example/users/cy", Note: "cy-note"}).Error)

	quiet := "quiet-node.example"
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: quiet, Severity: "silence", PrivateComment: "synthetic-private-remark"}).Error)
	require.NoError(t, conn.Create(&models.Account{Username: "dee", Domain: &quiet, OriginId: "https://quiet-node.example/users/dee", DisplayName: "Dee", Note: "dee-note"}).Error)

	noop := "noop-node.example"
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: noop, Severity: "noop"}).Error)
	require.NoError(t, conn.Create(&models.Account{Username: "eve", Domain: &noop, OriginId: "https://noop-node.example/users/eve", Note: "eve-note"}).Error)

	hidden := "hidden-node.example"
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: hidden, Severity: "suspend", Obfuscate: true, PrivateComment: "synthetic-private-remark", PublicComment: "synthetic-public-remark"}).Error)
	require.NoError(t, conn.Create(&models.Account{Username: "fay", Domain: &hidden, OriginId: "https://hidden-node.example/users/fay", IsBan: true, Note: "fay-note"}).Error)

	wild := "a.b.wild.test"
	require.NoError(t, conn.Create(&models.Account{Username: "wilde", Domain: &wild, OriginId: "https://a.b.wild.test/users/wilde", Note: "wilde-note"}).Error)
	alias := "alias.example"
	require.NoError(t, conn.Create(&models.Account{Username: "gia", Domain: &alias, OriginId: "https://origin-blocked.example/users/gia", DisplayName: "Gia", Note: "gia-note"}).Error)
	hiddenOrigin := "alias-hidden.example"
	require.NoError(t, conn.Create(&models.Account{Username: "hoa", Domain: &hiddenOrigin, OriginId: "https://hidden-origin.example/users/hoa", Note: "hoa-note"}).Error)
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: "hidden-origin.example", Severity: "suspend", Obfuscate: true, PrivateComment: "synthetic-private-remark"}).Error)

	personal := "personal-block.example"
	require.NoError(t, conn.Create(&models.Account{Username: "ida", Domain: &personal, OriginId: "https://personal-block.example/users/ida", DisplayName: "Ida", Note: "ida-note"}).Error)
	var ida, neo models.Account
	require.NoError(t, conn.Where("username = ? AND domain = ?", "ida", personal).First(&ida).Error)
	require.NoError(t, conn.Where("username = ? AND domain = ?", "neo", clear).First(&neo).Error)
	require.NoError(t, conn.Create(&models.Block{AccountId: viewer.Id, Type: models.BlockTypeDomain, TargetDomain: personal}).Error)
	require.NoError(t, conn.Create(&models.Block{AccountId: viewer.Id, Type: models.BlockTypeAccount, TargetAccountId: neo.Id}).Error)
	require.NoError(t, conn.Create(&models.MuteAccount{AccountId: viewer.Id, TargetId: neo.Id}).Error)

	bothDomain := "both.example"
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: bothDomain, Severity: "suspend"}).Error)
	require.NoError(t, conn.Create(&models.Account{Username: "jia", Domain: &bothDomain, OriginId: "https://both.example/users/jia", IsBan: true, Note: "jia-note"}).Error)

	code, raw, body := lookupJSON(t, "/api/v1/accounts/lookup?acct=luna", nil)
	require.Equal(t, 200, code)
	require.NotContains(t, raw, "availability")
	require.NotContains(t, raw, `"suspended"`)
	require.Equal(t, "luna", body["acct"])

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=suspendedlogin", nil)
	require.Equal(t, 200, code, raw)
	require.Equal(t, true, body["suspended"])
	require.Equal(t, models.ProfileAvailabilityAccountSuspended, availabilityOf(body))
	require.NotContains(t, raw, "synthetic-private-remark")

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=bannedremote@remote.example", nil)
	require.Equal(t, 200, code, raw)
	require.Equal(t, models.ProfileAvailabilityAccountSuspended, availabilityOf(body))
	require.Contains(t, raw, "remote-ban-note")

	withBlockedHosts(t, []string{"https://Blocked.Example/path", "*.wild.test", "origin-blocked.example"})
	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=ada@blocked.example", nil)
	require.Equal(t, 404, code, raw)
	require.Equal(t, "Record not found", body["error"])
	require.Equal(t, models.ProfileAvailabilityDomainSuspended, availabilityOf(body))
	require.NotContains(t, raw, "cached-ada-note")
	require.NotContains(t, raw, "Cached Ada")

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=ada@A.Blocked.Example", viewer)
	require.Equal(t, 404, code, raw)
	require.Equal(t, models.ProfileAvailabilityDomainSuspended, availabilityOf(body))
	require.NotContains(t, raw, "sub-ada-note")

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=wilde@a.b.wild.test", nil)
	require.Equal(t, 404, code, raw)
	require.Equal(t, models.ProfileAvailabilityDomainSuspended, availabilityOf(body))
	require.NotContains(t, raw, "wilde-note")

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=ada@notblocked.example", nil)
	require.Equal(t, 200, code, raw)
	require.Empty(t, availabilityOf(body))
	require.Contains(t, raw, "healthy-note")

	code, raw, _ = lookupJSON(t, "/api/v1/accounts/lookup?acct=nobody@notblocked.example", nil)
	require.Equal(t, 404, code)
	require.NotContains(t, raw, "availability")

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=ghost@blocked.example", nil)
	require.Equal(t, 404, code, raw)
	require.Equal(t, models.ProfileAvailabilityDomainSuspended, availabilityOf(body))

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=bea@user.db.example", nil)
	require.Equal(t, 404, code, raw)
	require.Equal(t, models.ProfileAvailabilityDomainSuspended, availabilityOf(body))
	require.NotContains(t, raw, "synthetic-private-remark")
	require.NotContains(t, raw, "synthetic-public-remark")
	require.NotContains(t, raw, "bea-note")

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=cy@empty-severity.example", nil)
	require.Equal(t, 404, code, raw)
	require.Equal(t, models.ProfileAvailabilityDomainSuspended, availabilityOf(body))

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=dee@quiet-node.example", nil)
	require.Equal(t, 200, code, raw)
	require.Empty(t, availabilityOf(body))
	require.Contains(t, raw, "dee-note")

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=eve@noop-node.example", nil)
	require.Equal(t, 200, code, raw)
	require.Empty(t, availabilityOf(body))

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=fay@hidden-node.example", nil)
	require.Equal(t, 404, code, raw)
	require.Empty(t, availabilityOf(body))
	require.NotContains(t, raw, "domain_suspended")
	require.NotContains(t, raw, "account_suspended")
	require.NotContains(t, raw, "synthetic-private-remark")
	require.NotContains(t, raw, "synthetic-public-remark")
	require.NotContains(t, raw, "fay-note")

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=gia@alias.example", nil)
	require.Equal(t, 200, code, raw)
	require.Equal(t, models.ProfileAvailabilityDomainSuspended, availabilityOf(body))
	require.NotEqual(t, true, body["suspended"])

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=hoa@alias-hidden.example", nil)
	require.Equal(t, 200, code, raw)
	require.Empty(t, availabilityOf(body))
	require.NotContains(t, raw, "synthetic-private-remark")
	require.Contains(t, raw, "hoa-note")

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=ida@personal-block.example", viewer)
	require.Equal(t, 404, code, raw)
	require.Empty(t, availabilityOf(body))
	require.NotContains(t, raw, "domain_suspended")
	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=ida@personal-block.example", nil)
	require.Equal(t, 200, code, raw)
	require.Empty(t, availabilityOf(body))
	require.Equal(t, "ida@personal-block.example", body["acct"])

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=neo@clear.example", viewer)
	require.Equal(t, 200, code, raw)
	require.Empty(t, availabilityOf(body))
	require.NotContains(t, raw, "availability")
	require.Contains(t, raw, "neo-note")

	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=jia@both.example", nil)
	require.Equal(t, 404, code, raw)
	require.Equal(t, models.ProfileAvailabilityDomainSuspended, availabilityOf(body))
	require.NotContains(t, raw, "account_suspended")
	require.NotContains(t, raw, "jia-note")

	require.NoError(t, conn.Where("domain = ?", bothDomain).Delete(&models.DomainBlock{}).Error)
	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=jia@both.example", nil)
	require.Equal(t, 200, code, raw)
	require.Equal(t, models.ProfileAvailabilityAccountSuspended, availabilityOf(body))

	require.NoError(t, conn.Where("LOWER(domain) = ?", strings.ToLower(dbDomain)).Delete(&models.DomainBlock{}).Error)
	code, raw, body = lookupJSON(t, "/api/v1/accounts/lookup?acct=bea@user.db.example", nil)
	require.Equal(t, 200, code, raw)
	require.Empty(t, availabilityOf(body))
	require.Contains(t, raw, "bea-note")
}
