package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

func ViewPoll(c *gin.Context) {
	poll, abort := getPoll(c)
	if abort {
		return
	}

	var currentAccount *models.Account

	if account, ok := c.Get("account"); ok {
		currentAccount = account.(*models.Account)
	}

	ret, err := serializer.ToPoll(poll, currentAccount)

	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(200, ret)
}

type VotePollRequest struct {
	Choices []int `json:"choices" binding:"required"`
}

func VotePoll(c *gin.Context) {
	req := &VotePollRequest{}
	err := c.ShouldBindJSON(req)

	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	poll, abort := getPoll(c)
	if abort {
		return
	}

	// 判断是否已经过期
	if poll.ExpiresAt.Before(db.GetDB().NowFunc()) {
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

	if err != nil {
		_ = c.Error(err)
		return
	}

	if voteCount > 0 {
		c.JSON(400, gin.H{"error": "already voted"})
		return
	}

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

	if err != nil {
		_ = c.Error(err)
		return
	}

	// 重新获取投票信息
	err = db.GetDB().Model(&models.Poll{}).Preload("Options").First(poll, poll.Id).Error

	if err != nil {
		_ = c.Error(err)
		return
	}

	ret, err := serializer.ToPoll(poll, currentAccount)

	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(200, ret)
}

func getPoll(c *gin.Context) (poll *models.Poll, abort bool) {
	pollIdStr := c.Param("id")

	if pollIdStr == "" {
		c.JSON(400, gin.H{"error": "poll id is required"})
		return
	}

	pollId, err := strconv.ParseUint(pollIdStr, 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "poll id must be numeric"})
		return
	}

	poll = &models.Poll{}
	err = db.GetDB().Model(&models.Poll{}).Preload("Options").First(poll, pollId).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "poll not found"})
			return
		}
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}

	status := &models.Status{}
	err = db.GetDB().Model(&models.Status{}).First(status, poll.StatusId).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "poll has been deleted"})
			return
		}
		_ = c.Error(err)
		return
	}

	var currentAccount *models.Account

	if account, ok := c.Get("account"); ok {
		currentAccount = account.(*models.Account)
	}

	canView, err := status.CanViewEx(db.GetDB(), currentAccount)

	if err != nil {
		_ = c.Error(err)
		return
	}

	if !canView {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}

	return
}
