package models

import (
	"regexp"
	"strings"
	"time"

	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 预编译正则表达式，避免每次请求时重复编译.
var (
	statusEmojiRegex   = regexp.MustCompile(`:[a-zA-Z0-9_]+:`)
	statusTagRegex     = regexp.MustCompile(`#\S+`)
	statusMentionRegex = regexp.MustCompile(`@\S+`)
)

const (
	StatusTypeStatus = iota
	StatusTypeRelog
	StatusTypeReply
	StatusTypeCard
	StatusTypePoll
)

const (
	StatusVisibilityPublic   = iota // 公开 - 全站可见
	StatusVisibilityUnlisted        // 对所有人可见，但不会出现在公共时间线上
	StatusVisibilityPrivate         // 私有 - 只有关注者可见
	StatusVisibilityDirect          // 只有提到的用户可见
)

type Status struct { //nolint:recvcheck
	Id            uint64        `gorm:"primary_key;autoIncrement" json:"id"`
	Content       StatusContent `json:"content"`
	AccountId     uint64        `gorm:"not null;index"            json:"account_id"`
	ApplicationId *uint64       `json:"application_id"`
	Visibility    uint8         `gorm:"type:tinyint;not null;default:0"        json:"visibility"`
	ReblogId      *uint64       `json:"reblog_id"`
	ReblogsCount  uint64        `gorm:"not null;default:0"        json:"reblogs_count"`
	RepliesCount  uint64        `gorm:"not null;default:0"        json:"replies_count"`

	InReplyToId        *uint64        `json:"in_reply_to_id"`
	InReplyToAccountId *uint64        `json:"in_reply_to_account_id"`
	Sensitive          bool           `gorm:"not null;default:false"        json:"sensitive"`
	Language           string         `gorm:"type:varchar(16)"              json:"language"`
	FavouritesCount    uint64         `gorm:"not null;default:0"            json:"favourites_count"`
	ScheduledAt        *time.Time     `gorm:"index"                         json:"scheduled_at"`
	OriginUri          *string        `gorm:"type:varchar(255)"             json:"origin_uri"`
	OriginUrl          *string        `gorm:"type:varchar(255)"             json:"origin_url"`
	CreatedAt          *time.Time     `gorm:"not null;autoCreateTime;index" json:"created_at"`
	UpdatedAt          *time.Time     `gorm:"not null;autoUpdateTime"       json:"updated_at"`
	DeletedAt          *time.Time     `gorm:"index"                         json:"-"`
	Properties         map[string]any `gorm:"type:json;serializer:json"     json:"properties"`
	Domain             *string        `gorm:"type:varchar(128);index"       json:"domain"`
	ConversationId     *uint64        `gorm:"index"                         json:"conversation_id"`

	Application  *Application `gorm:"foreignKey:application_id"           json:"application"`
	Attachments  Attachments  `gorm:"many2many:status_media_attachments;" json:"attachments"`
	ReplyAccount *Account     `gorm:"foreignKey:InReplyToAccountId"       json:"reply_account"`
	Emojis       Emojis       `gorm:"many2many:status_emojis;"            json:"emojis"`
	ToAccounts   Accounts     `gorm:"many2many:status_to_accounts;"       json:"to_accounts"`
	Poll         *Poll        `gorm:"foreignKey:status_id"                json:"poll"`
	Account      *Account     `gorm:"foreignKey:account_id"               json:"account"`
	Reblog       *Status      `gorm:"foreignKey:reblog_id"                json:"relog"`
	Tags         Tags         `gorm:"many2many:status_tags;"              json:"tags"`

	isLoadAllExtendFields bool `gorm:"-"`
	conversationIdChanged bool `gorm:"-"`
}

func (Status) TableName() string {
	return "statuses"
}

func (s *Status) SetAllExtendFieldsLoaded() {
	s.isLoadAllExtendFields = true
}

func (s *Status) CheckAndLoadAllExtendFields(tx *gorm.DB) (err error) {
	if s.isLoadAllExtendFields {
		return err
	}

	if s.ApplicationId != nil && *s.ApplicationId > 0 && s.Application == nil {
		s.Application = &Application{}
		err = tx.Model(s).Association("Application").Find(s.Application)
		if err != nil {
			return err
		}
	}

	if s.ReblogId != nil && *s.ReblogId > 0 && s.Reblog == nil {
		s.Reblog = &Status{}
		err = tx.Model(s).Association("Reblog").Find(s.Reblog)
		if err != nil {
			return err
		}
	}

	if s.Poll == nil {
		poll := &Poll{}

		err = tx.Model(poll).Preload("Options").Where("status_id = ?", s.Id).First(poll).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if err == nil {
			s.Poll = poll
		}

		err = nil
	}

	if s.AccountId > 0 && s.Account == nil {
		s.Account = &Account{}
		err = tx.Model(s).Association("Account").Find(s.Account)
		if err != nil {
			return err
		}
	}

	if s.InReplyToAccountId != nil && *s.InReplyToAccountId > 0 && s.ReplyAccount == nil {
		s.ReplyAccount = &Account{}
		err = tx.Model(s).Association("ReplyAccount").Find(s.ReplyAccount)
		if err != nil {
			return err
		}
	}

	if s.Emojis == nil {
		s.Emojis = make(Emojis, 0)
		err = tx.Model(s).Association("Emojis").Find(&s.Emojis)
		if err != nil {
			return err
		}
	}

	if s.Attachments == nil {
		s.Attachments = make(Attachments, 0)
		err = tx.Model(s).Association("Attachments").Find(&s.Attachments)
		if err != nil {
			return err
		}
	}

	if s.Tags == nil {
		s.Tags = make([]*Tag, 0)
		err = tx.Model(s).Association("Tags").Find(&s.Tags)
		if err != nil {
			return err
		}
	}

	if s.Content.Content == "" {
		err = tx.Model(s).Association("Content").Find(&s.Content)
		if err != nil {
			return err
		}
	}

	s.isLoadAllExtendFields = true

	return err
}

// BatchLoadAllExtendFields 批量加载多条 Status 的扩展字段，减少 N+1 查询问题。
// 对于切片中的每个 Status，如果尚未加载扩展字段，则统一使用 IN 查询批量加载。
func BatchLoadAllExtendFields(tx *gorm.DB, statuses []*Status) (err error) {
	if len(statuses) == 0 {
		return err
	}

	// 收集需要加载的 ID
	notLoaded := make([]*Status, 0, len(statuses))
	accountIDs := make([]uint64, 0)

	for _, s := range statuses {
		if !s.isLoadAllExtendFields {
			notLoaded = append(notLoaded, s)
			if s.AccountId > 0 {
				accountIDs = append(accountIDs, s.AccountId)
			}
		}
	}

	if len(notLoaded) == 0 {
		return err
	}

	// 批量加载 Accounts
	if len(accountIDs) > 0 {
		accounts := make([]*Account, 0, len(accountIDs))
		err = tx.Where("id IN ?", accountIDs).Find(&accounts).Error
		if err != nil {
			return err
		}

		accountMap := make(map[uint64]*Account, len(accounts))
		for _, a := range accounts {
			accountMap[a.Id] = a
		}

		for _, s := range notLoaded {
			if s.Account == nil && s.AccountId > 0 {
				if a, ok := accountMap[s.AccountId]; ok {
					s.Account = a
				}
			}
		}
	}

	// 对每条 status 调用单个加载（此时大部分关联已被预加载，查询单条很快）
	for _, s := range notLoaded {
		err = s.CheckAndLoadAllExtendFields(tx)
		if err != nil {
			return err
		}
	}

	return err
}

func (s *Status) BeforeSave(tx *gorm.DB) (err error) {
	event.GetEventManager("status").Trigger(event.Event{
		Name: "before_save", //nolint:goconst
		Data: s,
	})

	return
}

func (s *Status) AfterSave(tx *gorm.DB) (err error) {
	if err = s.SetConversation(tx); err != nil {
		return
	}

	if s.conversationIdChanged {
		err = tx.Table(s.TableName()).Where("id = ?", s.Id).Select("conversation_id").Update("conversation_id", s.ConversationId).Error
		if err != nil {
			return
		}

		s.conversationIdChanged = false
	}

	event.GetEventManager("status").Trigger(event.Event{
		Name: "after_save",
		Data: s,
	})

	return
}

func (s *Status) AfterCreate(tx *gorm.DB) (err error) {
	event.GetEventManager("status").Trigger(event.Event{
		Name: "after_create", //nolint:goconst
		Data: s,
	})

	return
}

func (s *Status) AfterUpdate(tx *gorm.DB) (err error) {
	event.GetEventManager("status").Trigger(event.Event{
		Name: "after_update", //nolint:goconst
		Data: s,
	})

	return
}

func (s *Status) BeforeDelete(tx *gorm.DB) (err error) {
	event.GetEventManager("status").Trigger(event.Event{
		Name: "before_delete_status",
		Data: s,
	})

	return
}

func (s *Status) AfterDelete(tx *gorm.DB) (err error) {
	event.GetEventManager("status").Trigger(event.Event{
		Name: "after_delete_status",
		Data: s,
	})

	return
}

func (s *Status) SetVisibilityByString(visibility string) (err error) {
	switch strings.ToLower(visibility) {
	case "public": //nolint:goconst
		s.Visibility = StatusVisibilityPublic
	case "unlisted": //nolint:goconst
		s.Visibility = StatusVisibilityUnlisted
	case "private": //nolint:goconst
		s.Visibility = StatusVisibilityPrivate
	case "direct": //nolint:goconst
		s.Visibility = StatusVisibilityDirect
	default:
		err = errors.New("invalid visibility")
	}

	return
}

func (s *Status) GetVisibilityString() string {
	switch s.Visibility {
	case StatusVisibilityPublic:
		return "public"
	case StatusVisibilityUnlisted:
		return "unlisted"
	case StatusVisibilityPrivate:
		return "private"
	case StatusVisibilityDirect:
		return "direct"
	default:
		return "unknown" //nolint:goconst
	}
}

func (s *Status) IsPinned(db *gorm.DB, account *Account) (ret bool, err error) {
	if err = db.Model(&PinStatus{}).Where("status_id = ? AND account_id = ?", s.Id, account.Id).First(&PinStatus{}).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = nil
		}

		return
	}

	ret = true

	return
}

func (s *Status) IsFavourited(db *gorm.DB, account *Account) (ret bool, err error) {
	if err = db.Model(&FavoriteStatus{}).Where("status_id = ? AND account_id = ?", s.Id, account.Id).First(&FavoriteStatus{}).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = nil
		}

		return
	}

	ret = true

	return
}

func (s *Status) IsReblogged(db *gorm.DB, account *Account) (ret bool, err error) {
	if err = db.Model(&Status{}).Where("reblog_id = ? AND account_id = ?", s.Id, account.Id).First(&Status{}).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = nil
		}

		return
	}

	ret = true

	return
}

func (s *Status) IsMuted(db *gorm.DB, account *Account) (ret bool, err error) {
	// 屏蔽单条动态
	muteStatus := &MuteStatus{}
	if err = db.Model(&MuteStatus{}).Where("account_id = ? AND status_id = ?", account.Id, s.Id).First(muteStatus).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = nil
		}
	} else {
		// 找到了 MuteStatus 记录
		ret = true
		return
	}

	if ret {
		return
	}

	// 屏蔽某个账号
	record := &MuteAccount{}
	if err = db.Model(&MuteAccount{}).Where("account_id = ? AND target_id = ?", account.Id, s.AccountId).First(record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = nil
		}

		return
	}

	if record.ExpiredAt != nil && record.ExpiredAt.Before(time.Now()) {
		ret = false

		return
	}

	ret = true

	return
}

func (s *Status) IsBookmarked(db *gorm.DB, account *Account) (ret bool, err error) {
	if err = db.Model(&BookmarkStatus{}).Where("status_id = ? AND account_id = ?", s.Id, account.Id).First(&BookmarkStatus{}).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = nil
		}

		return
	}

	ret = true

	return
}

// IsScheduled 是否是定时动态
// 如果 ScheduledAt 不为空，则是定时动态，到期的时候会自动发布（ScheduledAt 会被置空）.
func (s *Status) IsScheduled() bool {
	return s.ScheduledAt != nil
}

func (s *Status) Public(db *gorm.DB) (err error) {
	if s.Poll != nil {
		// 更新 expire_at
		if s.Poll.Properties != nil {
			if expireIn, ok := s.Poll.Properties["expires_in"].(uint64); ok {
				s.Poll.ExpiresAt = new(time.Now().Add(time.Duration(expireIn) * time.Second)) //nolint:gosec // G115: reviewed and safe
			}
		}

		err = db.Omit(clause.Associations).Save(s.Poll).Error
		if err != nil {
			return
		}
	}

	s.ScheduledAt = nil

	if err = db.Omit(clause.Associations).Save(s).Error; err != nil {
		return
	}

	return
}

func (s *Status) CanView(account *Account, myFollows map[uint64]*Follow) (canView bool) {
	if s.AccountId == account.Id {
		canView = true

		return canView
	}

	if s.IsScheduled() && (account == nil || s.AccountId != account.Id) {
		canView = false

		return canView
	}

	// 公开或者对所有人可见
	if s.Visibility == StatusVisibilityPublic || s.Visibility == StatusVisibilityUnlisted {
		canView = true

		return canView
	}

	// 有没提到我的
	isMention := false

	for _, a := range s.ToAccounts {
		if a.Id == account.Id {
			isMention = true

			break
		}
	}

	if s.Visibility == StatusVisibilityPrivate {
		if _, ok := myFollows[s.AccountId]; ok {
			canView = true

			return canView
		}

		if isMention {
			canView = true

			return canView
		}

		return canView
	}

	if s.Visibility == StatusVisibilityDirect && isMention {
		canView = true

		return canView
	}

	// 如果是 rebolg 的，检查原始状态
	if s.ReblogId != nil && *s.ReblogId > 0 && canView {
		if s.Reblog == nil {
			canView = false

			return canView
		}

		canView = s.Reblog.CanView(account, myFollows)

		return canView
	}

	return canView
}

func (s *Status) CanViewEx(db *gorm.DB, account *Account) (ok bool, err error) {
	if s.AccountId == account.Id {
		ok = true

		return
	}

	follows := make(Follows, 0)

	err = db.Scopes(Followed).Where("account_id = ?", account.Id).Find(&follows).Error
	if err != nil {
		err = errors.Wrap(err, "get follows failed")

		return
	}

	followMap := make(map[uint64]*Follow)
	for _, f := range follows {
		followMap[f.FollowId] = f
	}

	ok = s.CanView(account, followMap)

	return
}

func (s *Status) SetConversationId(id uint64) {
	if s.ConversationId != nil && *s.ConversationId == id {
		return
	}

	if s.ConversationId == nil && id == 0 {
		return
	}

	if id > 0 {
		s.ConversationId = &id
	} else {
		s.ConversationId = nil
	}

	s.conversationIdChanged = true
}

func (s *Status) SetConversation(tx *gorm.DB) (err error) {
	var conversation *Conversation

	if s.InReplyToId != nil && *s.InReplyToId > 0 {
		originStatus := &Status{}
		err = tx.Where("id = ?", *s.InReplyToId).Preload("Reblog").First(originStatus).Error
		if err != nil {
			err = errors.New("reply status not found")

			return err
		}

		if originStatus.ReblogId != nil && *originStatus.ReblogId > 0 {
			originStatus = originStatus.Reblog
		}

		// 如果被回复的 status 的 conversation_id 不为空，则使用被回复的 status 的 conversation_id,否则创建一个新的 conversation，并使用新的 conversation_id
		if originStatus.ConversationId == nil {
			conversation = &Conversation{}
			err = tx.Create(conversation).Error
			if err != nil {
				return err
			}

			originStatus.ConversationId = &conversation.Id

			err = tx.Select("conversation_id").Save(originStatus).Error
			if err != nil {
				return err
			}
		}

		s.SetConversationId(*originStatus.ConversationId)
	}

	// 如果 visibility 是 direct，则自动创建 conversation
	if s.Visibility == StatusVisibilityDirect && s.ConversationId == nil {
		conversation = &Conversation{}
		err = tx.Create(conversation).Error
		if err != nil {
			return err
		}

		s.SetConversationId(conversation.Id)
	}

	if conversation == nil && s.ConversationId != nil {
		conversation = &Conversation{}
		err = tx.Where("id = ?", *s.ConversationId).First(conversation).Error
		if err != nil {
			return err
		}
	}

	if conversation != nil {
		conversation.AppendStatus(s.Id)
		conversation.AppendAccount(s.AccountId)

		err = tx.Select("statuses_ids,accounts_ids").Save(conversation).Error
		if err != nil {
			return err
		}
	}

	return err
}

func StatusLoadAllQuery(query *gorm.DB) *gorm.DB {
	return query.Preload("Account").
		Preload("Content").
		Preload("Application").
		Preload("Attachments").
		Preload("ReplyAccount").
		Preload("Emojis").
		Preload("ToAccounts").
		Preload("Poll").
		Preload("Poll.Options").
		Preload("Poll.Options").
		Preload("Tags").
		Preload("Reblog").
		Preload("Reblog.Account").
		Preload("Reblog.Content").
		Preload("Reblog.Application").
		Preload("Reblog.Attachments").
		Preload("Reblog.ReplyAccount").
		Preload("Reblog.Emojis").
		Preload("Reblog.ToAccounts").
		Preload("Reblog.Poll").
		Preload("Reblog.Poll.Options").
		Preload("Reblog.Poll.Options").
		Preload("Reblog.Tags")
}

type Statuses []*Status

func (ss *Statuses) GetIds() (ids []uint64) {
	for _, s := range *ss {
		ids = append(ids, s.Id)
	}

	return
}

func (ss *Statuses) GetIdsWithoutReblogWarp() (ids []uint64) {
	for _, s := range *ss {
		if s.ReblogId == nil {
			ids = append(ids, s.Id)
		} else {
			ids = append(ids, *s.ReblogId)
		}
	}

	return
}

func (ss *Statuses) GetAccountIds() (ids []uint64) {
	ids = make([]uint64, 0, len(*ss))
	for _, s := range *ss {
		ids = append(ids, s.AccountId)
	}

	ids = utils.UniqueSlice(ids)

	return
}

func (ss *Statuses) Filter(filter func(s *Status) bool) (ret Statuses) {
	ret = make(Statuses, 0, len(*ss))
	for _, s := range *ss {
		if filter(s) {
			ret = append(ret, s)
		}
	}

	return
}

type StatusContent struct {
	StatusId    uint64         `gorm:"primary_key;auto_increment" json:"status_id"`
	Content     string         `gorm:"type:text;not null"         json:"content"`
	SpoilerText string         `gorm:"type:text;"                 json:"spoiler_text"`
	Card        map[string]any `gorm:"type:json;serializer:json"  json:"card"`
}

func (sc *StatusContent) TableName() string {
	return "status_contents"
}

func (sc *StatusContent) BeforeCreate(tx *gorm.DB) (err error) {
	err = sc.bindAnyThingsByStatusContent(tx, false)

	return
}

func (sc *StatusContent) BeforeUpdate(tx *gorm.DB) (err error) {
	err = sc.bindAnyThingsByStatusContent(tx, true)

	return
}

func (sc *StatusContent) bindAnyThingsByStatusContent(tx *gorm.DB, refresh bool) (err error) {
	if err = sc.bindEmojiByStatusContent(tx, refresh); err != nil {
		return
	}

	if err = sc.bindTagsByStatusContent(tx, refresh); err != nil {
		return
	}

	if err = sc.bindToAccountsByStatusContent(tx, refresh); err != nil {
		return
	}

	return
}

func (sc *StatusContent) bindEmojiByStatusContent(tx *gorm.DB, refresh bool) (err error) {
	if refresh {
		err = tx.Where("status_id = ?", sc.StatusId).Delete(&StatusEmoji{}).Error
		if err != nil {
			return err
		}
	}

	// emoji tag format: :emoji_name:
	// example: :ablobdj:​​:fes:​​:blobcatrainbow:​​:fes:​​:blobcatrainbow:​​:fes:​​:blobcatrainbow:​​:ablobdj:

	preg := statusEmojiRegex

	emojiNames := preg.FindAllString(sc.Content, -1)
	if len(emojiNames) == 0 {
		return err
	}

	emojiNames = utils.FilterDuplicateAndEmptyString(emojiNames)

	if len(emojiNames) == 0 {
		return err
	}

	for i, emojiName := range emojiNames {
		emojiNames[i] = strings.Trim(emojiName, ": \t\n\r")
	}

	emojis := make([]*Emoji, 0)
	if err = tx.Where("shortcode IN ?", emojiNames).Find(&emojis).Error; err != nil {
		return err
	}

	if len(emojis) == 0 {
		return err
	}

	statusEmojis := make([]*StatusEmoji, 0)
	for _, emoji := range emojis {
		statusEmojis = append(statusEmojis, &StatusEmoji{
			StatusId: sc.StatusId,
			EmojiId:  emoji.Id,
		})
	}

	err = tx.Create(&statusEmojis).Error

	return err
}

func (sc *StatusContent) bindTagsByStatusContent(tx *gorm.DB, refresh bool) (err error) {
	if refresh {
		// 把之前的tag的使用次数减一
		err = tx.Model(&Tag{}).Where("id IN (?)", tx.Model(&StatusTag{}).Select("tag_id").Where("status_id = ?", sc.StatusId)).
			Update("status_count", gorm.Expr("if(status_count > 0, status_count - ?, 0)", 1)).Error
		if err != nil {
			return err
		}

		err = tx.Where("status_id = ?", sc.StatusId).Delete(&StatusTag{}).Error
		if err != nil {
			return err
		}
	}

	// tag format: #tag_name
	// example: #abc #def #ghi #你好
	reg := statusTagRegex
	tagNames := reg.FindAllString(sc.Content, -1)

	if len(tagNames) == 0 {
		return err
	}

	tagNames = utils.FilterDuplicateAndEmptyString(tagNames)

	if len(tagNames) == 0 {
		return err
	}

	for i, tagName := range tagNames {
		tagNames[i] = strings.Trim(tagName, "# \t\n\r")
	}

	tags := make([]*Tag, 0)
	if err = tx.Where("name IN ?", tagNames).Find(&tags).Error; err != nil {
		return err
	}

	tagMap := make(map[string]*Tag)
	for _, tag := range tags {
		tagMap[tag.Name] = tag
	}

	// 找出不存在的tag
	notExistTagNames := make([]string, 0)

	for _, tagName := range tagNames {
		if _, ok := tagMap[tagName]; !ok {
			notExistTagNames = append(notExistTagNames, tagName)
		}
	}

	// 创建不存在的tag
	if len(notExistTagNames) > 0 {
		for _, tagName := range notExistTagNames {
			tag := &Tag{
				Name: tagName,
			}
			err = tx.Create(tag).Error
			if err != nil {
				return err
			}

			tagMap[tagName] = tag
		}
	}

	statusTags := make([]*StatusTag, 0)
	for _, tag := range tagMap {
		statusTags = append(statusTags, &StatusTag{
			StatusId: sc.StatusId,
			TagId:    tag.Id,
		})
	}

	err = tx.Create(&statusTags).Error

	// 刷新tag的使用次数
	for _, tag := range tagMap {
		err = tx.Model(tag).UpdateColumn("status_count", gorm.Expr("status_count + ?", 1)).Error
		if err != nil {
			return err
		}
	}

	return err
}

func (sc *StatusContent) bindToAccountsByStatusContent(tx *gorm.DB, refresh bool) (err error) {
	if refresh {
		err = tx.Where("status_id = ?", sc.StatusId).Delete(&StatusToAccount{}).Error
		if err != nil {
			return err
		}
	}

	// format: @username1 @username2 @username3@domain1 @username4@domain2
	reg := statusMentionRegex
	accountNames := reg.FindAllString(sc.Content, -1)

	if len(accountNames) == 0 {
		return err
	}

	accountNames = utils.FilterDuplicateAndEmptyString(accountNames)

	if len(accountNames) == 0 {
		return err
	}
	// 根据 domain 聚合
	domainAccountNames := make(map[string][]string)

	for _, accountName := range accountNames {
		accountName = strings.Trim(accountName, "@ \t\n\r")
		domain := ""
		var userName string

		if strings.Contains(accountName, "@") {
			parts := strings.Split(accountName, "@")
			if len(parts) == 2 {
				userName = parts[0]
				domain = parts[1]
			} else {
				continue
			}
		} else {
			userName = accountName
		}

		if userName == "" {
			continue
		}

		if _, ok := domainAccountNames[domain]; !ok {
			domainAccountNames[domain] = make([]string, 0)
		}

		domainAccountNames[domain] = append(domainAccountNames[domain], userName)
	}

	accounts := make([]*Account, 0)

	for domain, userNames := range domainAccountNames {
		_accounts := make([]*Account, 0)

		if domain == "" {
			err = tx.Where("username IN ?", userNames).Where("domain is null").Find(&_accounts).Error
		} else {
			err = tx.Where("username IN ?", userNames).Where("domain = ?", domain).Find(&_accounts).Error
		}

		if err != nil {
			return err
		}

		accounts = append(accounts, _accounts...)
	}

	if len(accounts) == 0 {
		return err
	}

	statusAccounts := make([]*StatusToAccount, 0)
	for _, account := range accounts {
		statusAccounts = append(statusAccounts, &StatusToAccount{
			StatusId:  sc.StatusId,
			AccountId: account.Id,
		})
	}

	err = tx.Create(&statusAccounts).Error

	return err
}

type StatusMediaAttachment struct {
	Id       uint64 `gorm:"primary_key;auto_increment" json:"id"`
	StatusId uint64 `gorm:"not null;index"             json:"status_id"`
	MediaId  uint64 `gorm:"not null;index"             json:"media_id"`
}

func (sma *StatusMediaAttachment) TableName() string {
	return "status_media_attachments"
}

type StatusTag struct {
	Id       uint64 `gorm:"primary_key;auto_increment" json:"id"`
	StatusId uint64 `gorm:"not null;index"             json:"status_id"`
	TagId    uint64 `gorm:"not null;index"             json:"tag_id"`
}

func (st *StatusTag) TableName() string {
	return "status_tags"
}

type StatusEmoji struct {
	Id       uint64 `gorm:"primary_key;auto_increment" json:"id"`
	StatusId uint64 `gorm:"not null;index"             json:"status_id"`
	EmojiId  uint64 `gorm:"not null;index"             json:"emoji_id"`
}

func (se *StatusEmoji) TableName() string {
	return "status_emojis"
}

type StatusToAccount struct {
	Id        uint64     `gorm:"primary_key;auto_increment"    json:"id"`
	StatusId  uint64     `gorm:"not null;index"                json:"status_id"`
	AccountId uint64     `gorm:"not null;index"                json:"account_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime;index" json:"created_at"`
	Account   *Account   `json:"account"`
}

func (StatusToAccount) TableName() string {
	return "status_to_accounts"
}

type FavoriteStatus struct {
	Id        uint64     `gorm:"primary_key;auto_increment"    json:"id"`
	AccountId uint64     `gorm:"not null;index"                json:"account_id"`
	StatusId  uint64     `gorm:"not null;index"                json:"status_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime;index" json:"created_at"`
	Status    *Status    `json:"status"`
}

func (fs *FavoriteStatus) TableName() string {
	return "favorite_statuses"
}

func (fs *FavoriteStatus) AfterCreate(tx *gorm.DB) (err error) {
	if err = tx.Model(&Status{}).Where("id", fs.StatusId).UpdateColumn("favourites_count", gorm.Expr("favourites_count + ?", 1)).Error; err != nil {
		return
	}

	event.Trigger("favourite", event.Event{
		Name: "favourite", //nolint:goconst
		Data: fs,
	})

	return
}

func (fs *FavoriteStatus) AfterDelete(tx *gorm.DB) (err error) {
	if err = tx.Model(&Status{}).Where("id", fs.StatusId).UpdateColumn("favourites_count", gorm.Expr("favourites_count - ?", 1)).Error; err != nil {
		return
	}

	event.Trigger("favourite", event.Event{
		Name: "unfavourite",
		Data: fs,
	})

	return
}

type BookmarkStatus struct {
	Id        uint64     `gorm:"primary_key;auto_increment"    json:"id"`
	AccountId uint64     `gorm:"not null;index"                json:"account_id"`
	StatusId  uint64     `gorm:"not null;index"                json:"status_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime;index" json:"created_at"`
}

func (bs *BookmarkStatus) TableName() string {
	return "bookmark_statuses"
}

type PinStatus struct {
	Id        uint64     `gorm:"primary_key;auto_increment"    json:"id"`
	AccountId uint64     `gorm:"not null;index"                json:"account_id"`
	StatusId  uint64     `gorm:"not null;index"                json:"status_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime;index" json:"created_at"`
}

func (ps *PinStatus) TableName() string {
	return "pin_statuses"
}

type StatusHistory struct {
	Id                  uint64     `gorm:"primary_key;auto_increment"    json:"id"`
	StatusId            uint64     `gorm:"not null;index"                json:"status_id"`
	AccountId           uint64     `gorm:"not null;index"                json:"account_id"`
	Content             string     `gorm:"type:text;not null"            json:"content"`
	SpoilerText         string     `gorm:"type:text;not null"            json:"spoiler_text"`
	Sensitive           bool       `gorm:"not null"                      json:"sensitive"`
	MediaAttachmentsIds []uint64   `gorm:"type:json;serializer:json"     json:"media_attachments_ids"`
	EmojisIds           []uint64   `gorm:"type:json;serializer:json"     json:"emojis_ids"`
	CreatedAt           *time.Time `gorm:"not null;autoCreateTime;index" json:"created_at"`
	Account             *Account   `gorm:"foreignKey:AccountId"          json:"account"`
}

func (StatusHistory) TableName() string {
	return "status_histories"
}
