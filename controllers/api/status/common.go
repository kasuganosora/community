package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm"
)

// abortUnlessVisible is the shared visibility gate of the status-scoped
// endpoints. Like Mastodon (StatusPolicy#show? failing raises
// RecordNotFound), a status the viewer may not see answers 404
// {"error":"Record not found"}, the same body as a missing id, so private and
// direct statuses cannot be probed for existence. Ownership checks on a
// visible status (delete, pin, mute) and scope checks keep their own codes.
func abortUnlessVisible(c *gin.Context, conn *gorm.DB, status *models.Status, viewer *models.Account) {
	if status.DeletedAt != nil {
		controllers.AbortRecordNotFound(c)
	}
	allowed, err := status.CanViewEx(conn, viewer)
	controllers.AbortIfError(c, err)
	if !allowed {
		controllers.AbortRecordNotFound(c)
	}
}

func getRealStatus(c *gin.Context) (status *models.Status) {
	status = controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)

	// 判断当前的 status 是否是转发的 status, 如果是则替换成原始的 status
	if status.ReblogId != nil && *status.ReblogId > 0 {
		if status.Reblog == nil {
			controllers.AbortRecordNotFound(c)
		}

		status = status.Reblog
	}

	return
}

func returnFullSingleStatus(c *gin.Context, status *models.Status, currentAccount *models.Account) {
	query := models.StatusLoadAllQuery(db.GetDB().Model(status))

	err := query.First(status, status.Id).Error
	if err != nil {
		_ = c.Error(err)

		controllers.AbortPanic()
	}

	ret, err := serializer.ToStatus(status, currentAccount)
	if err != nil {
		_ = c.Error(err)

		controllers.AbortPanic()
	}
	// Origin servers (Misskey family, Pleroma, Fedibird) do not push reactions
	// from third servers to us; show their counts on the detail view.
	if !status.IsLocal() {
		ret.EmojiReactions = activitypub.WithRemoteReactions(c.Request.Context(), status, ret.EmojiReactions)
	}

	c.JSON(200, ret)
}
