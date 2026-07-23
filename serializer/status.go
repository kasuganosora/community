package serializer

import (
	"fmt"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"strconv"
	"time"
)

type Status struct {
	Id                 string        `json:"id"`
	CreatedAt          *time.Time    `json:"created_at"`
	InReplyToId        *string       `json:"in_reply_to_id"`
	InReplyToAccountId *string       `json:"in_reply_to_account_id"`
	Sensitive          bool          `json:"sensitive"`
	SpoilerText        string        `json:"spoiler_text"`
	Visibility         string        `json:"visibility"`
	Language           string        `json:"language"`
	Uri                string        `json:"uri"`
	Url                string        `json:"url"`
	RepliesCount       uint64        `json:"replies_count"`
	ReblogsCount       uint64        `json:"reblogs_count"`
	FavouritesCount    uint64        `json:"favourites_count"`
	QuotesCount        uint64        `json:"quotes_count"`
	EditedAt           *time.Time    `json:"edited_at"`
	Favourited         bool          `json:"favourited"`
	Reblogged          bool          `json:"reblogged"`
	Muted              bool          `json:"muted"`
	Bookmarked         bool          `json:"bookmarked"`
	Pinned             bool          `json:"pinned"`
	Content            string        `json:"content"`
	Filtered           []string      `json:"filtered"`
	Reblog             *Status       `json:"reblog"`
	Application        *Application  `json:"application"`
	Account            *Account      `json:"account"`
	MediaAttachments   []*Attachment `json:"media_attachments"`
	Mentions           []*Mention    `json:"mentions"`
	Tags               []*Tag        `json:"tags"`
	Emojis             []*Emoji      `json:"emojis"`
	Card               Card          `json:"card"`
	Poll               *Poll         `json:"poll"`
	intId              uint64        `json:"-"`
}

func ToStatusBase(status *models.Status) (ret *Status, err error) {
	if status == nil {
		return ret, err
	}

	ret = &Status{}
	ret.Id = strconv.FormatUint(status.Id, 10)
	ret.intId = status.Id
	ret.CreatedAt = status.CreatedAt

	if status.InReplyToId != nil && *status.InReplyToId > 0 {
		ret.InReplyToId = new(string)
		*ret.InReplyToId = strconv.FormatUint(*status.InReplyToId, 10)
	}

	if status.InReplyToId != nil && *status.InReplyToId > 0 {
		ret.InReplyToAccountId = new(string)
		*ret.InReplyToAccountId = strconv.FormatUint(*status.InReplyToAccountId, 10)
	}

	ret.Sensitive = status.Sensitive
	ret.SpoilerText = status.Content.SpoilerText
	ret.Visibility = status.GetVisibilityString()

	ret.Language = status.Language
	if status.OriginUri != nil {
		ret.Uri = *status.OriginUri
	} else {
		ret.Uri = fmt.Sprintf("%s/%s/statuses/%d", systemsettings.GetString(systemsettings.KeyDomain, ""), status.Account.Username, status.Id)
	}

	if status.OriginUri != nil {
		ret.Url = *status.OriginUri
	} else {
		ret.Url = fmt.Sprintf("%s/@%s/%d", systemsettings.GetString(systemsettings.KeyDomain, ""), status.Account.Username, status.Id)
	}

	ret.RepliesCount = status.RepliesCount
	ret.ReblogsCount = status.ReblogsCount
	ret.FavouritesCount = status.FavouritesCount

	if status.UpdatedAt != nil || status.UpdatedAt.Unix() > status.CreatedAt.Unix() {
		ret.EditedAt = status.UpdatedAt
	}

	ret.Content = status.Content.Content

	ret.Reblog, err = ToStatusBase(status.Reblog)
	if err != nil {
		return ret, err
	}

	ret.Application = ToApplication(status.Application)
	ret.Mentions = ToMentions(status.ToAccounts)
	ret.Tags = ToTags(status.Tags)
	ret.Emojis = ToEmojis(status.Emojis)
	ret.Card = status.Content.Card
	ret.Account = ToAccount(status.Account)

	return ret, err
}

func ToStatus(status *models.Status, currentAccount *models.Account) (ret *Status, err error) {
	ret, err = ToStatusBase(status)
	if err != nil {
		return ret, err
	}

	if status.Reblog != nil {
		ret.Reblog, err = ToStatus(status.Reblog, currentAccount)
		if err != nil {
			return ret, err
		}
	}

	if status.Poll != nil {
		ret.Poll, err = ToPoll(status.Poll, currentAccount)
		if err != nil {
			return ret, err
		}
	}

	if currentAccount == nil {
		return ret, err
	}

	dbConn := db.GetDB()

	if status.ReblogId == nil || *status.ReblogId == 0 {
		var isPined, isReblogged, isFavourited, isMuted, isBookmarked bool
		if isPined, err = status.IsPinned(dbConn, currentAccount); err != nil {
			return ret, err
		}

		if isReblogged, err = status.IsReblogged(dbConn, currentAccount); err != nil {
			return ret, err
		}

		if isFavourited, err = status.IsFavourited(dbConn, currentAccount); err != nil {
			return ret, err
		}

		if isMuted, err = status.IsMuted(dbConn, currentAccount); err != nil {
			return ret, err
		}

		if isBookmarked, err = status.IsBookmarked(dbConn, currentAccount); err != nil {
			return ret, err
		}

		ret.Pinned = isPined
		ret.Reblogged = isReblogged
		ret.Favourited = isFavourited
		ret.Muted = isMuted
		ret.Bookmarked = isBookmarked
	}

	filters, err := models.GetFilters(dbConn, currentAccount.Id)
	if err != nil {
		return ret, err
	}

	ret.Filtered = filters.GetStatusFilters(status, currentAccount).GetFilterActionsName()

	return ret, err
}

func ToStatuses(statuses models.Statuses, currentAccount *models.Account) (ret []*Status, err error) {
	ret = make([]*Status, 0, len(statuses))

	if statuses == nil {
		return ret, err
	}

	dbConn := db.GetDB()
	statusIds := statuses.GetIdsWithoutReblogWarp()

	var filters models.Filters

	muteAccounts := make(models.MuteAccounts, 0)
	blocks := make(models.Blocks, 0)
	bookmarkedCheckMap := make(map[uint64]bool)
	rebloggedCheckMap := make(map[uint64]bool)
	favouritedCheckMap := make(map[uint64]bool)
	pinnedCheckMap := make(map[uint64]bool)
	muteCheckMap := make(map[uint64]bool)

	if currentAccount != nil {
		bookmarkedIds := make([]uint64, 0, len(statusIds))
		rebloggedIds := make([]uint64, 0, len(statusIds))
		favouritedIds := make([]uint64, 0, len(statusIds))
		pinnedIds := make([]uint64, 0, len(statusIds))
		muteIds := make([]uint64, 0, len(statusIds))
		// 找出这些状态那些是我置顶过的
		err = dbConn.Model(&models.PinStatus{}).Where("account_id = ? AND status_id IN (?)", currentAccount.Id, statusIds).Pluck("status_id", &pinnedIds).Error
		if err != nil {
			return ret, err
		}

		// 找出这些状态那些是我点赞过的
		err = dbConn.Model(&models.FavoriteStatus{}).Where("account_id = ? AND status_id IN (?)", currentAccount.Id, statusIds).Pluck("status_id", &favouritedIds).Error
		if err != nil {
			return ret, err
		}

		// 找出这些状态那些是我转发过的
		err = dbConn.Model(&models.Status{}).Where("account_id = ? AND reblog_id IN (?)", currentAccount.Id, statusIds).Pluck("reblog_id", &rebloggedIds).Error
		if err != nil {
			return ret, err
		}

		// 那些是我加入书签的
		err = dbConn.Model(&models.BookmarkStatus{}).Where("account_id = ? AND status_id IN (?)", currentAccount.Id, statusIds).Pluck("status_id", &bookmarkedIds).Error
		if err != nil {
			return ret, err
		}

		// 我有那些过滤器
		filters, err = models.GetFilters(dbConn, currentAccount.Id)
		if err != nil {
			return ret, err
		}

		// 我屏蔽了那些用户
		err = dbConn.Model(&models.MuteAccount{}).Where("account_id = ?", currentAccount.Id).Find(&muteAccounts).Error
		if err != nil {
			return ret, err
		}

		// 我block了那些用户
		err = dbConn.Model(&models.Block{}).Where("account_id = ? or account_id = 0", currentAccount.Id).Find(&blocks).Error
		if err != nil {
			return ret, err
		}

		// 我屏蔽了那些状态
		err = dbConn.Model(&models.MuteStatus{}).Where("account_id = ? AND status_id IN (?)", currentAccount.Id, statusIds).Pluck("status_id", &muteIds).Error
		if err != nil {
			return ret, err
		}

		bookmarkedCheckMap = utils.GetExistsMap(bookmarkedIds)
		bookmarkedIds = nil
		rebloggedCheckMap = utils.GetExistsMap(rebloggedIds)
		rebloggedIds = nil
		favouritedCheckMap = utils.GetExistsMap(favouritedIds)
		favouritedIds = nil
		pinnedCheckMap = utils.GetExistsMap(pinnedIds)
		pinnedIds = nil
		muteCheckMap = utils.GetExistsMap(muteIds)
		muteIds = nil
	} else {
		// 没有登陆下
		filters, err = models.GetFilters(dbConn, 0)
		if err != nil {
			return ret, err
		}
	}

	muteAccounts = muteAccounts.ExcludeExpired()

	polls := make([]*models.Poll, 0)

	for _, status := range statuses {
		if status.Poll != nil {
			polls = append(polls, status.Poll)
		}
	}

	sPolls, err := ToPollsMapByStatusId(polls, currentAccount)
	if err != nil {
		return ret, err
	}

	for _, status := range statuses {
		if blocks.IsBlocked(status.Account) {
			continue
		}

		var retStatus *Status

		retStatus, err = ToStatusBase(status)
		// 只要是那个人发的，或者是转发的，都不要
		if muteAccounts.IsMuted(status.AccountId) || (status.Reblog != nil && muteAccounts.IsMuted(status.Reblog.AccountId)) {
			retStatus.Muted = true
		}

		if utils.IsExistsForCheckMap(muteCheckMap, status.Id) {
			retStatus.Muted = true
		}

		if err != nil {
			return nil, err
		}

		retStatus.Pinned = utils.IsExistsForCheckMap(pinnedCheckMap, status.Id)
		retStatus.Reblogged = utils.IsExistsForCheckMap(rebloggedCheckMap, status.Id)
		retStatus.Favourited = utils.IsExistsForCheckMap(favouritedCheckMap, status.Id)
		retStatus.Bookmarked = utils.IsExistsForCheckMap(bookmarkedCheckMap, status.Id)
		retStatus.Filtered = filters.GetStatusFilters(status, currentAccount).GetFilterActionsName()

		if status.Poll != nil {
			if sPoll, ok := sPolls[status.Id]; ok {
				retStatus.Poll = sPoll
			}
		}

		ret = append(ret, retStatus)
	}

	return ret, err
}
