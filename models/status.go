package models

import (
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"regexp"
	"strings"
	"time"
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

type Status struct {
	Id            uint64        `gorm:"primary_key;autoIncrement" json:"id"`
	Content       StatusContent `json:"content"`
	AccountId     uint64        `gorm:"not null;index" json:"account_id"`
	ApplicationId *uint64       `json:"application_id"`
	Visibility    uint8         `gorm:"not null;default:0" json:"visibility"`
	ReblogId      *uint64       `json:"reblog_id"`
	ReblogsCount  uint64        `gorm:"not null;default:0" json:"relogs_count"`
	RepliesCount  uint64        `gorm:"not null;default:0" json:"replies_count"`

	InReplyToId        *uint64        `json:"in_reply_to_id"`
	InReplyToAccountId *uint64        `json:"in_reply_to_account_id"`
	Sensitive          bool           `gorm:"not null;default:false" json:"sensitive"`
	Language           string         `gorm:"type:varchar(16)" json:"language"`
	FavouritesCount    uint64         `gorm:"not null;default:0" json:"favourites_count"`
	ScheduledAt        *time.Time     `gorm:"index" json:"scheduled_at"`
	OriginUri          *string        `gorm:"type:varchar(255)" json:"origin_uri"`
	OriginUrl          *string        `gorm:"type:varchar(255)" json:"origin_url"`
	CreatedAt          *time.Time     `gorm:"not null;autoCreateTime;index" json:"created_at"`
	UpdatedAt          *time.Time     `gorm:"not null;autoUpdateTime" json:"updated_at"`
	DeletedAt          *time.Time     `gorm:"index" json:"-"`
	Properties         map[string]any `gorm:"type:json;serializer:json" json:"properties"`
	Domain             *string        `gorm:"type:varchar(128);index" json:"domain"`

	Application  *Application `gorm:"foreignKey:application_id" json:"application"`
	Attachments  Attachments  `gorm:"many2many:status_media_attachments;" json:"attachments"`
	ReplyAccount *Account     `gorm:"foreignKey:InReplyToAccountId" json:"reply_account"`
	Emojis       Emojis       `gorm:"many2many:status_emojis;" json:"emojis"`
	ToAccounts   Accounts     `gorm:"many2many:status_to_accounts;" json:"to_accounts"`
	Poll         *Poll        `gorm:"foreignKey:status_id" json:"poll"`
	Account      *Account     `gorm:"foreignKey:account_id" json:"account"`
	Reblog       *Status      `gorm:"foreignKey:reblog_id" json:"relog"`
	Tags         []*Tag       `gorm:"many2many:status_tags;" json:"tags"`

	isLoadAllExtendFields bool `gorm:"-"`
}

func (s *Status) TableName() string {
	return "statuses"
}

func (s *Status) SetAllExtendFieldsLoaded() {
	s.isLoadAllExtendFields = true
}

func (s *Status) CheckAndLoadAllExtendFields(tx *gorm.DB) (err error) {
	if s.isLoadAllExtendFields {
		return
	}

	if s.ApplicationId != nil && *s.ApplicationId > 0 && s.Application == nil {
		s.Application = &Application{}
		if err = tx.Model(s).Association("Application").Find(s.Application); err != nil {
			return
		}
	}

	if s.ReblogId != nil && *s.ReblogId > 0 && s.Reblog == nil {
		s.Reblog = &Status{}
		if err = tx.Model(s).Association("Reblog").Find(s.Reblog); err != nil {
			return
		}
	}

	if s.Poll == nil {
		poll := &Poll{}
		err = tx.Model(poll).Preload("Options").Where("status_id = ?", s.Id).First(poll).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}

		if err == nil {
			s.Poll = poll
		}

		err = nil
	}

	if s.AccountId > 0 && s.Account == nil {
		s.Account = &Account{}
		if err = tx.Model(s).Association("Account").Find(s.Account); err != nil {
			return
		}
	}

	if s.InReplyToAccountId != nil && *s.InReplyToAccountId > 0 && s.ReplyAccount == nil {
		s.ReplyAccount = &Account{}
		if err = tx.Model(s).Association("ReplyAccount").Find(s.ReplyAccount); err != nil {
			return
		}
	}

	if s.Emojis == nil {
		s.Emojis = make(Emojis, 0)
		if err = tx.Model(s).Association("Emojis").Find(&s.Emojis); err != nil {
			return
		}
	}

	if s.Attachments == nil {
		s.Attachments = make(Attachments, 0)
		if err = tx.Model(s).Association("Attachments").Find(&s.Attachments); err != nil {
			return
		}
	}

	if s.Tags == nil {
		s.Tags = make([]*Tag, 0)
		if err = tx.Model(s).Association("Tags").Find(&s.Tags); err != nil {
			return
		}
	}

	if s.Content.Content == "" {
		if err = tx.Model(s).Association("Content").Find(&s.Content); err != nil {
			return
		}
	}

	s.isLoadAllExtendFields = true
	return
}

func (s *Status) BeforeSave(tx *gorm.DB) (err error) {
	if s.InReplyToId != nil && *s.InReplyToId > 0 {
		if err = tx.Where("id = ?", *s.InReplyToId).First(&Status{}).Error; err != nil {
			err = errors.New("reply status not found")
			return
		}
	}

	event.GetEventManager("status").Trigger(event.Event{
		Name: "before_save_status",
		Data: s,
	})

	return
}

func (s *Status) AfterSave(tx *gorm.DB) (err error) {
	event.GetEventManager("status").Trigger(event.Event{
		Name: "after_save_status",
		Data: s,
	})

	return
}

func (s *Status) AfterCreate(tx *gorm.DB) (err error) {
	event.GetEventManager("status").Trigger(event.Event{
		Name: "after_create_status",
		Data: s,
	})

	return
}

func (s *Status) AfterUpdate(tx *gorm.DB) (err error) {
	event.GetEventManager("status").Trigger(event.Event{
		Name: "after_update_status",
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
	case "public":
		s.Visibility = StatusVisibilityPublic
	case "unlisted":
		s.Visibility = StatusVisibilityUnlisted
	case "private":
		s.Visibility = StatusVisibilityPrivate
	case "direct":
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
		return "unknown"
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

	// 屏蔽单条动态
	muteStatus := &MuteStatus{}
	if err = db.Model(&MuteStatus{}).Where("account_id = ? AND status_id = ?", account.Id, s.Id).First(muteStatus).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = nil
		}
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
// 如果 ScheduledAt 不为空，则是定时动态，到期的时候会自动发布（ScheduledAt 会被置空）
func (s *Status) IsScheduled() bool {
	return s.ScheduledAt != nil
}

func (s *Status) Public(db *gorm.DB) (err error) {
	if s.Poll != nil {
		// 更新 expire_at
		expireIn := s.Poll.Properties["expires_in"].(uint64)
		s.Poll.ExpiresAt = utils.GetPtr(time.Now().Add(time.Duration(expireIn) * time.Second))

		if err = db.Omit(clause.Associations).Save(s.Poll).Error; err != nil {
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
		return
	}

	if s.IsScheduled() && (account == nil || s.AccountId != account.Id) {
		canView = false
		return
	}

	// 公开或者对所有人可见
	if s.Visibility == StatusVisibilityPublic || s.Visibility == StatusVisibilityUnlisted {
		canView = true
		return
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
			return
		}

		if isMention {
			canView = true
			return
		}

		return
	}

	if s.Visibility == StatusVisibilityDirect && isMention {
		canView = true
		return
	}

	// 如果是 rebolg 的，检查原始状态
	if s.ReblogId != nil && *s.ReblogId > 0 && canView == true {
		if s.Reblog == nil {
			canView = false
			return
		}

		canView = s.Reblog.CanView(account, myFollows)
		return
	}

	return
}

func (s *Status) CanViewEx(db *gorm.DB, account *Account) (ok bool, err error) {
	if s.AccountId == account.Id {
		ok = true
		return
	}

	follows := make(Follows, 0)
	err = db.Where("account_id = ?", account.Id).Find(&follows).Error
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
	Content     string         `gorm:"type:text;not null" json:"content"`
	SpoilerText string         `gorm:"type:text;" json:"spoiler_text"`
	Card        map[string]any `gorm:"type:json;serializer:json" json:"card"`
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
		if err = tx.Where("status_id = ?", sc.StatusId).Delete(&StatusEmoji{}).Error; err != nil {
			return
		}
	}

	// emoji tag format: :emoji_name:
	// example: :ablobdj:​​:fes:​​:blobcatrainbow:​​:fes:​​:blobcatrainbow:​​:fes:​​:blobcatrainbow:​​:ablobdj:

	preg := regexp.MustCompile(`:[a-zA-Z0-9_]+:`)
	emojiNames := preg.FindAllString(sc.Content, -1)
	if len(emojiNames) == 0 {
		return
	}

	emojiNames = utils.FilterDuplicateAndEmptyString(emojiNames)

	if len(emojiNames) == 0 {
		return
	}

	for i, emojiName := range emojiNames {
		emojiNames[i] = strings.Trim(emojiName, ": \t\n\r")
	}

	emojis := make([]*Emoji, 0)
	if err = tx.Where("shortcode IN ?", emojiNames).Find(&emojis).Error; err != nil {
		return
	}

	if len(emojis) == 0 {
		return
	}

	statusEmojis := make([]*StatusEmoji, 0)
	for _, emoji := range emojis {
		statusEmojis = append(statusEmojis, &StatusEmoji{
			StatusId: sc.StatusId,
			EmojiId:  emoji.Id,
		})
	}

	err = tx.Create(&statusEmojis).Error
	return
}

func (sc *StatusContent) bindTagsByStatusContent(tx *gorm.DB, refresh bool) (err error) {
	if refresh {
		// 把之前的tag的使用次数减一
		err = tx.Model(&Tag{}).Where("id IN (?)", tx.Model(&StatusTag{}).Select("tag_id").Where("status_id = ?", sc.StatusId)).
			Update("status_count", gorm.Expr("if(status_count > 0, status_count - ?, 0)", 1)).Error

		if err != nil {
			return
		}

		if err = tx.Where("status_id = ?", sc.StatusId).Delete(&StatusTag{}).Error; err != nil {
			return
		}
	}

	// tag format: #tag_name
	// example: #abc #def #ghi #你好
	reg := regexp.MustCompile(`#\S+`)
	tagNames := reg.FindAllString(sc.Content, -1)

	if len(tagNames) == 0 {
		return
	}

	tagNames = utils.FilterDuplicateAndEmptyString(tagNames)

	if len(tagNames) == 0 {
		return
	}

	for i, tagName := range tagNames {
		tagNames[i] = strings.Trim(tagName, "# \t\n\r")
	}

	tags := make([]*Tag, 0)
	if err = tx.Where("name IN ?", tagNames).Find(&tags).Error; err != nil {
		return
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
			if err = tx.Create(tag).Error; err != nil {
				return
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
		if err = tx.Model(tag).UpdateColumn("status_count", gorm.Expr("status_count + ?", 1)).Error; err != nil {
			return
		}
	}
	return
}

func (sc *StatusContent) bindToAccountsByStatusContent(tx *gorm.DB, refresh bool) (err error) {

	if refresh {
		if err = tx.Where("status_id = ?", sc.StatusId).Delete(&StatusToAccount{}).Error; err != nil {
			return
		}
	}

	// format: @username1 @username2 @username3@domain1 @username4@domain2
	reg := regexp.MustCompile(`@\S+`)
	accountNames := reg.FindAllString(sc.Content, -1)

	if len(accountNames) == 0 {
		return
	}

	accountNames = utils.FilterDuplicateAndEmptyString(accountNames)

	if len(accountNames) == 0 {
		return
	}
	// 根据 domain 聚合
	domainAccountNames := make(map[string][]string)
	for _, accountName := range accountNames {
		accountName = strings.Trim(accountName, "@ \t\n\r")
		domain := ""
		userName := ""

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
			return
		}

		accounts = append(accounts, _accounts...)
	}

	if len(accounts) == 0 {
		return
	}

	statusAccounts := make([]*StatusToAccount, 0)
	for _, account := range accounts {
		statusAccounts = append(statusAccounts, &StatusToAccount{
			StatusId:  sc.StatusId,
			AccountId: account.Id,
		})
	}

	err = tx.Create(&statusAccounts).Error
	return
}

type StatusMediaAttachment struct {
	Id       uint64 `gorm:"primary_key;auto_increment" json:"id"`
	StatusId uint64 `gorm:"not null;index" json:"status_id"`
	MediaId  uint64 `gorm:"not null;index" json:"media_id"`
}

func (sma *StatusMediaAttachment) TableName() string {
	return "status_media_attachments"
}

type StatusTag struct {
	Id       uint64 `gorm:"primary_key;auto_increment" json:"id"`
	StatusId uint64 `gorm:"not null;index" json:"status_id"`
	TagId    uint64 `gorm:"not null;index" json:"tag_id"`
}

func (st *StatusTag) TableName() string {
	return "status_tags"
}

type StatusEmoji struct {
	Id       uint64 `gorm:"primary_key;auto_increment" json:"id"`
	StatusId uint64 `gorm:"not null;index" json:"status_id"`
	EmojiId  uint64 `gorm:"not null;index" json:"emoji_id"`
}

func (se *StatusEmoji) TableName() string {
	return "status_emojis"
}

type StatusToAccount struct {
	Id        uint64     `gorm:"primary_key;auto_increment" json:"id"`
	StatusId  uint64     `gorm:"not null;index" json:"status_id"`
	AccountId uint64     `gorm:"not null;index" json:"account_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime;index" json:"created_at"`
	Account   *Account   `json:"account"`
}

func (StatusToAccount) TableName() string {
	return "status_to_accounts"
}

type FavoriteStatus struct {
	Id        uint64     `gorm:"primary_key;auto_increment" json:"id"`
	AccountId uint64     `gorm:"not null;index" json:"account_id"`
	StatusId  uint64     `gorm:"not null;index" json:"status_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime;index" json:"created_at"`
}

func (fs *FavoriteStatus) TableName() string {
	return "favorite_statuses"
}

type BookmarkStatus struct {
	Id        uint64     `gorm:"primary_key;auto_increment" json:"id"`
	AccountId uint64     `gorm:"not null;index" json:"account_id"`
	StatusId  uint64     `gorm:"not null;index" json:"status_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime;index" json:"created_at"`
}

func (bs *BookmarkStatus) TableName() string {
	return "bookmark_statuses"
}

type PinStatus struct {
	Id        uint64     `gorm:"primary_key;auto_increment" json:"id"`
	AccountId uint64     `gorm:"not null;index" json:"account_id"`
	StatusId  uint64     `gorm:"not null;index" json:"status_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime;index" json:"created_at"`
}

func (ps *PinStatus) TableName() string {
	return "pin_statuses"
}

type StatusHistory struct {
	Id                  uint64     `gorm:"primary_key;auto_increment" json:"id"`
	StatusId            uint64     `gorm:"not null;index" json:"status_id"`
	AccountId           uint64     `gorm:"not null;index" json:"account_id"`
	Content             string     `gorm:"type:text;not null" json:"content"`
	SpoilerText         string     `gorm:"type:text;not null" json:"spoiler_text"`
	Sensitive           bool       `gorm:"not null" json:"sensitive"`
	MediaAttachmentsIds []uint64   `gorm:"type:json;serializer:json" json:"media_attachments_ids"`
	EmojisIds           []uint64   `gorm:"type:json;serializer:json" json:"emojis_ids"`
	CreatedAt           *time.Time `gorm:"not null;autoCreateTime;index" json:"created_at"`
	Account             *Account   `gorm:"foreignKey:AccountId" json:"account"`
}

func (StatusHistory) TableName() string {
	return "status_histories"
}
