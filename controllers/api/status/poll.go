package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm"
	"time"
)

func ViewPoll(c *gin.Context) {
	poll, abort := getPoll(c)
	if abort {
		return
	}

	currentAccount := controllers.GetAccount(c)

	ret, err := serializer.ToPoll(poll, currentAccount)
	controllers.AbortIfError(c, err)

	c.JSON(200, ret)
}

type VotePollRequest struct {
	Choices []int `json:"choices" binding:"required"`
}

func VotePoll(c *gin.Context) {
	req := &VotePollRequest{}
	err := c.ShouldBindJSON(req)

	controllers.AbortIf(c, err != nil, 400, gin.H{"error": err})

	poll, abort := getPoll(c)
	if abort {
		return
	}

	// 判断是否已经过期
	if poll.ExpiresAt != nil && poll.ExpiresAt.Before(time.Now()) {
		c.JSON(400, gin.H{"error": "poll expired"})
		return
	}

	// 判断单选还是多选
	if !poll.Multiple && len(req.Choices) > 1 {
		c.JSON(400, gin.H{"error": "poll is single choice"})
		return
	}

	// 判断选项是否存在
	for _, choice := range req.Choices {
		if choice < 0 || choice >= len(poll.Options) {
			c.JSON(400, gin.H{"error": "invalid choice"})
			return
		}
	}

	currentAccount := controllers.MustGetAccount(c)

	// 判断是否已经投过票
	var voteCount int64
	err = db.GetDB().Model(&models.PollOptionVote{}).Where("poll_id = ? and account_id = ?", poll.Id, currentAccount).Count(&voteCount).Error

	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, voteCount > 0, 400, gin.H{"error": "already voted"})

	voteOptions := make([]*models.PollOptionVote, 0, len(req.Choices))
	// 开始投票
	for _, choice := range req.Choices {
		voteOptions = append(voteOptions, &models.PollOptionVote{
			PollId:    poll.Id,
			OptionId:  poll.Options[choice].Id,
			AccountId: currentAccount.Id,
		})
	}

	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		// 插入投票记录
		err = tx.Model(&models.PollOptionVote{}).Create(voteOptions).Error
		if err != nil {
			return err
		}

		// 更新投票数
		for _, voteOption := range voteOptions {
			err = tx.Model(&models.PollOption{}).Where("id = ?", voteOption.OptionId).Update("vote_count", gorm.Expr("vote_count + 1")).Error
			if err != nil {
				return err
			}
		}

		return nil
	})

	controllers.AbortIfError(c, err)

	// 重新获取投票信息
	err = db.GetDB().Model(&models.Poll{}).Preload("Options").First(poll, poll.Id).Error

	controllers.AbortIfError(c, err)

	ret, err := serializer.ToPoll(poll, currentAccount)

	controllers.AbortIfError(c, err)

	c.JSON(200, ret)
}

func getPoll(c *gin.Context) (poll *models.Poll, abort bool) {
	poll = controllers.GetIdFromContext(&models.Poll{}, c, db.GetDB(), func(db *gorm.DB) *gorm.DB {
		return db.Preload("Options")
	})

	status := &models.Status{}
	err := db.GetDB().Model(&models.Status{}).First(status, poll.StatusId).Error
	controllers.AbortIfError(c, err)

	currentAccount := controllers.GetAccount(c)
	canView, err := status.CanViewEx(db.GetDB(), currentAccount)
	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, !canView, 403, gin.H{"error": "forbidden"})

	return
}
