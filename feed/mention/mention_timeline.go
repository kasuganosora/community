package mention

import (
	"context"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"log"
	"strconv"
	"time"
)

func init() {
	// 监听 所有关于status的事件
	event.Register("status", "*", &event.Handler{Exec: func(event event.Event) bool {
		if feed.DurableFanoutEnabled() {
			return true
		}
		if event.Data == nil {
			return false
		}

		status := event.Data.(*models.Status) //nolint:forcetypeassert
		if status == nil {
			return false
		}

		// 如果是新增的事件
		switch event.Name {
		case "after_create":
			if status.IsProfileBackfill() {
				return true
			}
			go func() {
				err := Push(status, false, true)
				if err != nil {
					log.Printf("mention_timeline: Push failed: %v", err)
				}
			}()
		case "before_update":
			go func() { // 更新之前，先删除
				err := Unpush(status, true)
				if err != nil {
					log.Printf("mention_timeline: Unpush failed: %v", err)
				}
			}()
		case "after_update":
			go func() {
				err := Push(status, true, true)
				if err != nil {
					log.Printf("mention_timeline: Push failed: %v", err)
				}
			}()
		case "after_delete":
			go func() {
				err := Unpush(status, true)
				if err != nil {
					log.Printf("mention_timeline: Unpush failed: %v", err)
				}
			}()
		}

		return true
	}})
}

type MentionTimeline struct {
	Account *models.Account
}

func (timeline *MentionTimeline) GetKeyFormat() string {
	return "feed:mention:%s"
}

func (timeline *MentionTimeline) GetTimelineId() string {
	return strconv.FormatUint(timeline.Account.Id, 10)
}

func (timeline *MentionTimeline) Rebuild() error {
	return timeline.RebuildContext(context.Background())
}

func (timeline *MentionTimeline) RebuildContext(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	lease, leaseErr := feed.BeginRebuild(ctx, timeline)
	if leaseErr != nil {
		return leaseErr
	}
	defer feed.ReleaseRebuild(ctx, timeline, lease)

	statuses := make(models.Statuses, 0)
	if err := mentionCandidates(db.GetDB().WithContext(ctx), timeline.Account.Id).
		Order(feed.StatusTimelineOrder).Limit(feed.MAX_ITEMS / 2).Find(&statuses).Error; err != nil {
		return err
	}
	return feed.PublishRebuild(ctx, timeline, lease, statuses, timeline.AggregateReblogs())
}

func (timeline *MentionTimeline) AggregateReblogs() bool {
	if timeline.Account.User != nil && timeline.Account.User.Settings != nil {
		return timeline.Account.User.Settings.GetAggregatesReblogs()
	}
	return false
}

// Apply publication and membership predicates before LIMIT, including on the
// database fallback. Limiting the mention subquery would truncate all history.
func mentionCandidates(tx *gorm.DB, accountID uint64) *gorm.DB {
	mentions := tx.Session(&gorm.Session{NewDB: true}).Model(&models.StatusToAccount{}).
		Where("account_id = ?", accountID).Select("status_id")
	return feed.EligibleTimelineStatuses(tx.Model(&models.Status{})).Where("statuses.id IN (?)", mentions)
}

func Push(status *models.Status, update bool, pushToClient bool) (err error) {
	accounts := make(models.Accounts, 0)

	// 只要本地用户的
	err = db.GetDB().Model(
		&models.Account{}).Preload("User").Preload("User.Settings").
		Where("id in (?)", db.GetDB().Model(
			&models.StatusToAccount{}).Where("status_id = ?", status.Id).Select("account_id")).
		Where("domain is null").
		Find(
			&accounts).Error
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return nil
	}

	method := "update" // https://docs.joinmastodon.org/methods/streaming/ 上说 创建是 update，更新是 status.update
	if update {
		method = "status.update"
	}

	isReblog := status.ReblogId != nil && *status.ReblogId > 0

	if !isReblog {
		// 非转发：使用批量添加优化性能（Redis Pipeline）
		// 注意：非转发情况下 aggregateReblogs 参数不影响结果，因为 reblog 分支不会被进入
		timelines := make([]feed.Timeline, 0, len(accounts))
		for _, account := range accounts {
			timelines = append(timelines, &MentionTimeline{Account: account})
		}

		err = feed.BatchAddToTimelineItems(timelines, status, false)

		if err != nil {
			return err
		}

		for _, timeline := range timelines {
			err = feed.TrimTimeline(timeline)
			if err != nil {
				return err
			}
		}
	} else {
		// 转发：保留单条调用，因为每个用户可能有不同的 aggregatesReblogs 设置
		for _, account := range accounts {
			timeline := &MentionTimeline{Account: account}

			aggregatesReblogs := false
			if account.User != nil && account.User.Settings != nil {
				aggregatesReblogs = account.User.Settings.GetAggregatesReblogs()
			}

			err = feed.AddToTimelineItem(timeline, status, aggregatesReblogs)

			if err != nil {
				return err
			}

			err = feed.TrimTimeline(timeline)

			if err != nil {
				return err
			}
		}
	}

	if pushToClient {
		for _, account := range accounts {
			timeline := &MentionTimeline{Account: account}
			if feed.PushUpdateRequired(timeline) {
				err = feed.PushToClient(timeline, method, status)
				if err != nil {
					return err
				}
			}
		}
	}

	return err
}

func Unpush(status *models.Status, pushToClient bool) (err error) {
	// 从数据库查询该 status 的所有被 mention 的本地用户账户。
	// 不依赖 status.ToAccounts，因为 before_update 事件触发时
	// status.ToAccounts 可能尚未加载（仍为旧值或为空），
	// 直接从数据库读取确保能获取到正确的旧 mention 列表。
	accounts := make(models.Accounts, 0)
	err = db.GetDB().Model(
		&models.Account{}).Preload("User").Preload("User.Settings").
		Where("id in (?)", db.GetDB().Model(
			&models.StatusToAccount{}).Where("status_id = ?", status.Id).Select("account_id")).
		Where("domain is null").
		Find(
			&accounts).Error
	if err != nil {
		return err
	}

	for _, account := range accounts {
		timeline := &MentionTimeline{Account: account}
		var aggregatesReblogs bool
		if account.User != nil && account.User.Settings != nil {
			aggregatesReblogs = account.User.Settings.GetAggregatesReblogs()
		}
		err = feed.RemoveTimelineItem(timeline, status, aggregatesReblogs)
		if err != nil {
			return err
		}

		err = feed.TrimTimeline(timeline)

		if err != nil {
			return err
		}

		if feed.PushUpdateRequired(timeline) && pushToClient {
			err = feed.PushToClient(timeline, "delete", status)
			if err != nil {
				return err
			}
		}
	}

	return err
}

func GetTimelineContent(account *models.Account, params *feed.ListParam) (statuses models.Statuses, err error) {
	timeline := &MentionTimeline{Account: account}
	if params == nil {
		params = &feed.ListParam{}
	} else {
		copyParams := *params
		params = &copyParams
	}
	params.Viewer = account
	newFilterCondition := func(tx *gorm.DB) (*gorm.DB, error) {
		return mentionCandidates(tx, account.Id), nil
	}
	return feed.GetStatusTimelineContent(timeline, params, newFilterCondition, nil)
}
