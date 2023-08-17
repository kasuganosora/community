package streaming

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	listFeed "github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/models"
	"strconv"
)

type ListStreamingRequest struct {
	OnlyMedia bool   `form:"only_media"`
	List      string `form:"list" binding:"required"`
}

func ListStreaming(c *gin.Context) {
	var req ListStreamingRequest
	var err error

	if err = c.ShouldBindQuery(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var listId uint64
	if listId, err = strconv.ParseUint(req.List, 10, 64); err != nil {
		c.JSON(400, gin.H{"error": "invalid list id"})
		return
	}

	var account *models.Account
	if _account, _ := c.Get("account"); account == nil {
		account = _account.(*models.Account)
	}

	list := &models.List{}

	// 如果 List是保密的话，则只有关注了这个List的人才能看到
	canview, err := models.ListCanView(list, account)
	if err != nil {
		_ = c.Error(err)
		return
	}

	if !canview {
		c.JSON(403, gin.H{"error": "you can't view this list"})
		return
	}

	if err = db.GetDB().Find(list, listId).Error; err != nil {
		c.JSON(400, gin.H{"error": "invalid list id"})
		return
	}

	timeline := &listFeed.ListTimeline{
		List: list,
	}

	procHandle := func(data any) (event, payload string, err error) {
		return serializerStatusEvent(account, data.(string))
	}

	startSseService(c, procHandle, timeline)

	return
}
