package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

func Bookmark(c *gin.Context) {
	status := getRealStatus(c)
	currentAccount := controllers.MustGetAccount(c)
	dbConn := db.GetDB()
	// 判断用户是否有权限收藏这条 status， 能看到都能收藏
	abortUnlessVisible(c, dbConn, status, currentAccount)

	// 是否已经收藏
	isBookmarked, err := status.IsBookmarked(dbConn, currentAccount)
	controllers.AbortIfError(c, err)

	// Mastodon: bookmarking is idempotent and returns the status.
	if isBookmarked {
		returnFullSingleStatus(c, status, currentAccount)

		return
	}

	bookmark := &models.BookmarkStatus{}
	bookmark.AccountId = currentAccount.Id
	bookmark.StatusId = status.Id

	err = dbConn.Create(bookmark).Error
	controllers.AbortIfError(c, err)

	returnFullSingleStatus(c, status, currentAccount)
}

func Unbookmark(c *gin.Context) {
	status := getRealStatus(c)

	currentAccount := controllers.MustGetAccount(c)
	dbConn := db.GetDB()

	// 判断用户是否有权限收藏这条 status， 能看到都能收藏
	abortUnlessVisible(c, dbConn, status, currentAccount)

	// 是否已经收藏
	isBookmarked, err := status.IsBookmarked(dbConn, currentAccount)
	controllers.AbortIfError(c, err)

	// Mastodon: unbookmarking a status that is not bookmarked is a no-op
	// that returns the status.
	if !isBookmarked {
		returnFullSingleStatus(c, status, currentAccount)

		return
	}

	err = dbConn.Delete(&models.BookmarkStatus{}, "account_id = ? AND status_id = ?", currentAccount.Id, status.Id).Error
	if err != nil {
		_ = c.Error(err)

		return
	}

	returnFullSingleStatus(c, status, currentAccount)
}
