package status

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	Choices []int `binding:"required" json:"choices"`
}

var (
	errPollGone     = errors.New("poll gone")
	errAlreadyVoted = errors.New("already voted")
)

// Mastodon-compatible Validation failed messages for POST /api/v1/polls/:id/votes.
const (
	errMsgAlreadyVoted  = "Validation failed: You have already voted on this poll"
	errMsgPollEnded     = "Validation failed: The poll has already ended"
	errMsgInvalidChoice = "Validation failed: The chosen vote option does not exist"
)

func VotePoll(c *gin.Context) {
	req := &VotePollRequest{}
	err := controllers.BindBody(c, req)

	controllers.AbortIf(c, err != nil, 400, gin.H{"error": err}) //nolint:goconst

	poll, status, abort := loadPoll(c)
	if abort {
		return
	}

	// Mastodon: 422 when the poll has already ended.
	if poll.IsClosed(time.Now()) {
		c.JSON(422, gin.H{"error": errMsgPollEnded})

		return
	}

	// Mastodon rejects multiple votes on a single-choice poll via the same
	// already-voted validation (creating the second choice fails under lock).
	if !poll.Multiple && len(req.Choices) > 1 {
		c.JSON(422, gin.H{"error": errMsgAlreadyVoted})

		return
	}

	// Mastodon: invalid / out-of-range choice index.
	for _, choice := range req.Choices {
		if choice < 0 || choice >= len(poll.Options) {
			c.JSON(422, gin.H{"error": errMsgInvalidChoice})

			return
		}
	}

	// 判断是否有选择
	if len(req.Choices) == 0 {
		c.JSON(400, gin.H{"error": "no choices provided"})

		return
	}

	currentAccount := controllers.MustGetAccount(c)

	// A repeated choice would count one voter twice for the same option.
	choices := make([]int, 0, len(req.Choices))
	picked := map[int]bool{}
	for _, choice := range req.Choices {
		if !picked[choice] {
			picked[choice] = true
			choices = append(choices, choice)
		}
	}

	voteOptions := make([]*models.PollOptionVote, 0, len(choices))
	names := make([]string, 0, len(choices))
	for _, choice := range choices {
		voteOptions = append(voteOptions, &models.PollOptionVote{
			PollId:    poll.Id,
			OptionId:  poll.Options[choice].Id,
			AccountId: currentAccount.Id,
		})
		names = append(names, poll.Options[choice].Content)
	}
	remote := status.Account != nil && status.Account.Domain != nil

	// Keep the transaction to a few indexed statements: it holds the poll row
	// lock that serializes votes on this poll (and its deletion). Federation
	// is only recorded here; the outbox worker builds the Update snapshot and
	// its recipient list later (models.QueuePollUpdate).
	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		// Lock order polls -> poll_options, as in recordRemoteVote, closePoll
		// and status deletion. The already-voted check runs under the lock
		// so concurrent requests of one account cannot both vote.
		var locked models.Poll
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", poll.Id).Take(&locked).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errPollGone
			}
			return err
		}

		var voteCount int64
		if err := tx.Model(&models.PollOptionVote{}).Where("poll_id = ? AND account_id = ?", poll.Id, currentAccount.Id).Count(&voteCount).Error; err != nil {
			return err
		}
		if voteCount > 0 {
			return errAlreadyVoted
		}

		// 插入投票记录
		if err := tx.Model(&models.PollOptionVote{}).Create(voteOptions).Error; err != nil {
			return err
		}

		// 更新投票数
		for _, voteOption := range voteOptions {
			if err := tx.Model(&models.PollOption{}).Where("id = ?", voteOption.OptionId).UpdateColumn("votes", gorm.Expr("votes + 1")).Error; err != nil {
				return err
			}
		}

		// Federation: a remote poll gets our vote (Create Note per choice);
		// a local poll sends its new tallies to followers and voters.
		if remote {
			return models.QueuePollVote(tx, currentAccount, status, names)
		}
		return models.QueuePollUpdate(tx, status.Id, false)
	})

	switch {
	case errors.Is(err, errPollGone):
		c.JSON(404, gin.H{"error": "Record not found"})
		return
	case errors.Is(err, errAlreadyVoted):
		// Concurrent race and plain re-vote share Mastodon's 422 message.
		c.JSON(422, gin.H{"error": errMsgAlreadyVoted})
		return
	}
	controllers.AbortIfError(c, err)

	// 重新获取投票信息
	err = db.GetDB().Model(&models.Poll{}).Preload("Options").First(poll, poll.Id).Error

	controllers.AbortIfError(c, err)

	ret, err := serializer.ToPoll(poll, currentAccount)

	controllers.AbortIfError(c, err)

	c.JSON(200, ret)
}

func getPoll(c *gin.Context) (poll *models.Poll, abort bool) {
	poll, _, abort = loadPoll(c)
	return
}

// loadPoll resolves :id to a poll the viewer may see, with its status (and
// the status author) for the vote path.
func loadPoll(c *gin.Context) (poll *models.Poll, status *models.Status, abort bool) {
	poll = controllers.GetIdFromContext(&models.Poll{}, c, db.GetDB(), func(db *gorm.DB) *gorm.DB {
		return db.Preload("Options")
	})

	status = &models.Status{}
	query := models.StatusLoadAllQuery(db.GetDB().Model(&models.Status{}))
	err := query.First(status, poll.StatusId).Error
	controllers.AbortIfError(c, err)

	// Mastodon hides polls the viewer cannot see (404, not 403), for
	// anonymous viewers as well as signed-in ones.
	abortUnlessVisible(c, db.GetDB(), status, controllers.GetAccount(c))

	return
}
