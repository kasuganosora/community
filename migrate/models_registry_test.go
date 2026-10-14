package migrate_test

import (
	activitypub "github.com/kasuganosora/community/activitypub"
	adminwebhook "github.com/kasuganosora/community/adminwebhook"
	accountapi "github.com/kasuganosora/community/controllers/api/account"
	oauthapi "github.com/kasuganosora/community/controllers/api/oauth"
	datatransfer "github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/migrate"
	models "github.com/kasuganosora/community/models"
)

// appModels lists every GORM model of the application whose table the
// migration chain must create. Matrix homeserver models are owned by
// homeserver.Models() (created when the homeserver starts) and are checked
// separately. TestEveryTableModelIsRegistered fails when a new type with a
// TableName method is added without being listed here.
var appModels = []any{
	&migrate.Migration{},
	&activitypub.Delivery{},
	&activitypub.ProcessedActivity{},
	&adminwebhook.Delivery{},
	&adminwebhook.Subscription{},
	&models.EmailDomainBlock{},
	&models.StatusCard{},
	&models.RecommendationFeedback{},
	&accountapi.Endorsement{},
	&accountapi.FeaturedTag{},
	&accountapi.FollowedTag{},
	&accountapi.PushSubscription{},
	&oauthapi.OAuthApplication{},
	&oauthapi.OAuthAuthorizationCode{},
	&datatransfer.ImportReceipt{},
	&datatransfer.ImportedMediaAlias{},
	&datatransfer.Job{},
	&datatransfer.StoredPreservedRecord{},
	&models.Account{},
	&models.AccountConversation{},
	&models.AccountEmoji{},
	&models.AccountExtra{},
	&models.AccountFederationKey{},
	&models.AccountKey{},
	&models.AdminAuditLog{},
	&models.Advertisement{},
	&models.Announcement{},
	&models.AnnouncementDismissal{},
	&models.Application{},
	&models.ApplicationUserAuth{},
	&models.AuthToken{},
	&models.AvatarDecoration{},
	&models.Block{},
	&models.BookmarkStatus{},
	&models.Contact{},
	&models.ContactRequest{},
	&models.Conversation{},
	&models.DomainBlock{},
	&models.Emoji{},
	&models.EmojiUpload{},
	&models.FavoriteStatus{},
	&models.FederationOutbox{},
	&models.FeedEvent{},
	&models.FeedBatch{},
	&models.Filter{},
	&models.Follow{},
	&models.Group{},
	&models.GroupMember{},
	&models.GroupMessage{},
	&models.InviteCode{},
	&models.List{},
	&models.ListAccount{},
	&models.ListFollowAccount{},
	&models.Marker{},
	&models.MatrixMessage{},
	&models.MatrixRoom{},
	&models.MatrixUser{},
	&models.MediaAttachment{},
	&models.MisskeyAuthSession{},
	&models.MuteAccount{},
	&models.MuteStatus{},
	&models.Notification{},
	&models.PasskeyCredential{},
	&models.PinStatus{},
	&models.Poll{},
	&models.PollOption{},
	&models.PollOptionVote{},
	&models.ProxyFollow{},
	&models.Relay{},
	&models.RemoteActorIdentity{},
	&models.RemoteReactionActivity{},
	&models.Report{},
	&models.Role{},
	&models.RoleUser{},
	&models.Rule{},
	&models.Status{},
	&models.StatusContent{},
	&models.StatusEmoji{},
	&models.StatusHistory{},
	&models.StatusReaction{},
	&models.StatusTag{},
	&models.StatusToAccount{},
	&models.SystemSetting{},
	&models.Tag{},
	&models.Thumbnail{},
	&models.ThumbnailConfig{},
	&models.ThumbnailJob{},
	&models.ThumbnailStats{},
	&models.User{},
	&models.UserMeta{},
	&models.UserSecretData{},
	&models.UserSetting{},
	&models.WebAuthnSession{},
}

// unmigratedModels are table models that intentionally have no table after
// the migrations, keyed by "<package dir>.<type>".
var unmigratedModels = map[string]string{
	// Legacy join table, dropped by UnifyMediaAttachmentsScript; kept for its Down.
	"models.StatusMediaAttachment": "dropped legacy table",
	// Snapshot row structs of individual migrations.
	"migrate/scripts.*": "migration snapshots",
}
