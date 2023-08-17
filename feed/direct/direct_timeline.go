package direct

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"strconv"
)

func init() {
	// 监听 所有关于status的事件
	event.Register("status", "*", &event.Handler{Exec: func(event event.Event) bool {
		if event.Data == nil {
			return false
		}
		status := event.Data.(*models.Status)
		if status == nil {
			return false
		}

		// 只处理 私信
		if status.Visibility != models.StatusVisibilityDirect {
			return true
		}

		//如果是新增的事件
		switch event.Name {
		case "after_create":
			go Push(status, false, true)
		case "before_update":
			go Unpush(status, true) // 更新之前，先删除
		case "after_update":
			go Push(status, true, true)
		case "after_delete":
			go Unpush(status, true)
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
		return
	}

	if status.ConversationId == nil {
		return
	}

	if status.ToAccounts == nil {
		status.ToAccounts = make([]*models.Account, 0)
		err = db.GetDB().Model(status).Association("ToAccounts").Find(&status.ToAccounts)
		if err != nil {
			return
		}
	}

	if status.ToAccounts == nil {
		return
	}

	conversation := &models.Conversation{}
	err = db.GetDB().Where("id = ?", *status.ConversationId).First(conversation).Error

	for _, account := range status.ToAccounts {
		timeline := &DirectTimeline{Account: account}
		if feed.PushUpdateRequired(timeline) {
			if err = feed.PushToClient(timeline, "conversation", conversation); err != nil {
				return
			}
		}
	}

	return
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
			if err = feed.PushToClient(timeline, "delete", status); err != nil {
				return
			}
		}
	}
	return
}
