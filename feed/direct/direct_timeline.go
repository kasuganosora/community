package direct

import (
	"log"
	"strconv"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
)

func init() {
	// 监听 所有关于status的事件
	event.Register("status", "*", &event.Handler{Exec: func(event event.Event) bool {
		if event.Data == nil {
			return false
		}

		status := event.Data.(*models.Status) //nolint:forcetypeassert
		if status == nil {
			return false
		}

		// 只处理 私信
		if status.Visibility != models.StatusVisibilityDirect {
			return true
		}

		// 如果是新增的事件
		switch event.Name {
		case "after_create":
			go func() {
				err := Push(status, false, true)
				if err != nil {
					log.Printf("direct_timeline: Push failed: %v", err)
				}
			}()
		case "before_update":
			go func() { // 更新之前，先删除
				err := Unpush(status, true)
				if err != nil {
					log.Printf("direct_timeline: Unpush failed: %v", err)
				}
			}()
		case "after_update":
			go func() {
				err := Push(status, true, true)
				if err != nil {
					log.Printf("direct_timeline: Push failed: %v", err)
				}
			}()
		case "after_delete":
			go func() {
				err := Unpush(status, true)
				if err != nil {
					log.Printf("direct_timeline: Unpush failed: %v", err)
				}
			}()
		}

		return true
	}})
}

type DirectTimeline struct {
	Account *models.Account
}

func (timeline *DirectTimeline) GetKeyFormat() string {
	return "direct:%s"
}

func (timeline *DirectTimeline) GetTimelineId() string {
	return strconv.FormatUint(timeline.Account.Id, 10)
}

func (timeline *DirectTimeline) Rebuild() (err error) {
	// 不需要重建，使用直接查询方式，这个只用作推送到 redis channel 使用
	return nil
}

func Push(status *models.Status, update bool, pushToClient bool) (err error) {
	if status.Visibility != models.StatusVisibilityDirect || !pushToClient {
		return err
	}

	if status.ConversationId == nil {
		return err
	}

	if status.ToAccounts == nil {
		status.ToAccounts = make([]*models.Account, 0)

		err = db.GetDB().Model(status).Association("ToAccounts").Find(&status.ToAccounts)
		if err != nil {
			return err
		}
	}

	if status.ToAccounts == nil {
		return err
	}

	conversation := &models.Conversation{}

	err = db.GetDB().Where("id = ?", *status.ConversationId).First(conversation).Error
	if err != nil {
		return err
	}

	for _, account := range status.ToAccounts {
		timeline := &DirectTimeline{Account: account}
		if feed.PushUpdateRequired(timeline) {
			err = feed.PushToClient(timeline, "conversation", conversation)
			if err != nil {
				return err
			}
		}
	}

	return err
}

func Unpush(status *models.Status, pushToClient bool) (err error) {
	if status.Visibility != models.StatusVisibilityDirect || !pushToClient {
		return
	}

	if status.ToAccounts == nil {
		status.ToAccounts = make([]*models.Account, 0)

		err = db.GetDB().Model(status).Association("ToAccounts").Find(&status.ToAccounts)
		if err != nil {
			return
		}
	}

	if len(status.ToAccounts) == 0 {
		return
	}

	for _, account := range status.ToAccounts {
		timeline := &DirectTimeline{Account: account}
		if feed.PushUpdateRequired(timeline) {
			err = feed.PushToClient(timeline, "delete", status)
			if err != nil {
				return
			}
		}
	}

	return
}
