package streaming

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	listFeed "github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"strconv"
)

type ListStreamingRequest struct {
	OnlyMedia bool   `form:"only_media"`
	List      string `binding:"required" form:"list"`
}

func ListStreaming(c *gin.Context) {
	var (
		req ListStreamingRequest
		err error
	)

	err = c.ShouldBindQuery(&req)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": err}) //nolint:goconst

	var listId uint64

	listId, err = strconv.ParseUint(req.List, 10, 64)
	controllers.AbortIf(c, err != nil || listId == 0, 422, gin.H{"error": "invalid list id"})

	account := controllers.GetAccount(c)

	list := &models.List{}

	err = db.GetDB().First(list, listId).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		controllers.AbortIf(c, true, 404, gin.H{"error": "list not found"})
	}
	controllers.AbortIfError(c, err)

	// 如果 List是保密的话，则只有关注了这个List的人才能看到
	canview, err := models.ListCanView(list, account)
	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, !canview, 403, gin.H{"error": "you can't view this list"})

	timeline := &listFeed.ListTimeline{
		List:      list,
		OnlyMedia: req.OnlyMedia,
	}

	procHandle := func(data any) (event, payload string, err error) {
		return serializerStatusEvent(account, data.(string)) //nolint:forcetypeassert
	}

	startSseService(c, procHandle, timeline)
}
