package serializer

import (
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// DetailedTag is returned by endpoints that serve a Tag entity. Embedded
// status tags keep their lighter name/URL representation.
type DetailedTag struct {
	*Tag
	Id        string       `json:"id"`
	History   []TagHistory `json:"history"`
	Following *bool        `json:"following,omitempty"`
	Featuring *bool        `json:"featuring,omitempty"`
}

func ToDetailedTags(conn *gorm.DB, tags []*models.Tag, viewer *models.Account) ([]DetailedTag, error) {
	now := time.Now().UTC().Truncate(24 * time.Hour)
	start := now.AddDate(0, 0, -6)
	result := make([]DetailedTag, 0, len(tags))
	ids := make([]uint64, 0, len(tags))
	for _, tag := range tags {
		if tag.Id != 0 {
			ids = append(ids, tag.Id)
		}
	}

	following, featuring, err := TagRelationshipFlags(conn, ids, viewer)
	if err != nil {
		return nil, err
	}
	type dailyUse struct {
		TagID    uint64
		Day      int
		Uses     uint64
		Accounts uint64
	}
	type tagDay struct {
		tag uint64
		day int
	}
	byTagDay := make(map[tagDay]dailyUse)
	if len(ids) > 0 {
		// Compare instants to UTC midnights. DATE(created_at) alone would
		// group by the database session's local day on MySQL and PostgreSQL.
		column := "statuses.created_at"
		boundary := func(day time.Time) any { return day }
		if dialect.IsSQLite(conn) {
			// SQLite timestamps can retain the offset they were written with.
			column = "DATE(statuses.created_at)"
			boundary = func(day time.Time) any { return day.Format(time.DateOnly) }
		}
		var bucket strings.Builder
		bucket.WriteString("CASE")
		bounds := make([]any, 0, 6)
		for i := 0; i < 6; i++ {
			bucket.WriteString(" WHEN " + column + " >= ? THEN " + strconv.Itoa(i))
			bounds = append(bounds, boundary(now.AddDate(0, 0, -i)))
		}
		bucket.WriteString(" ELSE 6 END")
		var rows []dailyUse
		err := conn.Table("status_tags").
			Select("status_tags.tag_id, "+bucket.String()+" AS day, COUNT(DISTINCT statuses.id) AS uses, COUNT(DISTINCT statuses.account_id) AS accounts", bounds...).
			Joins("JOIN statuses ON statuses.id = status_tags.status_id").
			Joins("JOIN accounts ON accounts.id = statuses.account_id").
			Where("status_tags.tag_id IN ? AND statuses.visibility = ?", ids, models.StatusVisibilityPublic).
			Where(column+" >= ? AND "+column+" < ?", boundary(start), boundary(now.AddDate(0, 0, 1))).
			Where("statuses.deleted_at IS NULL AND statuses.scheduled_at IS NULL AND accounts.deleted_at IS NULL").
			Where("accounts.is_ban = ? AND accounts.require_signin_to_view = ?", false, false).
			Group("status_tags.tag_id, day").Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			byTagDay[tagDay{row.TagID, row.Day}] = row
		}
	}

	for _, tag := range tags {
		row := DetailedTag{Tag: ToTag(tag), History: make([]TagHistory, 0, 7)}
		if viewer != nil {
			row.Following = new(following[tag.Id])
			row.Featuring = new(featuring[tag.Id])
		}
		if tag.Id != 0 {
			row.Id = strconv.FormatUint(tag.Id, 10)
		}
		for i := 0; i < 7; i++ {
			day := now.AddDate(0, 0, -i)
			usage := byTagDay[tagDay{tag.Id, i}]
			row.History = append(row.History, TagHistory{
				Day: strconv.FormatInt(day.Unix(), 10), Uses: strconv.FormatUint(usage.Uses, 10),
				Accounts: strconv.FormatUint(usage.Accounts, 10),
			})
		}
		result = append(result, row)
	}
	return result, nil
}

func TagRelationshipFlags(conn *gorm.DB, ids []uint64, viewer *models.Account) (following, featuring map[uint64]bool, err error) {
	if len(ids) == 0 || viewer == nil {
		return nil, nil, nil
	}
	load := func(table string) (map[uint64]bool, error) {
		var matched []uint64
		if err := conn.Table(table).Where("account_id = ? AND tag_id IN ?", viewer.Id, ids).Pluck("tag_id", &matched).Error; err != nil {
			return nil, err
		}
		result := make(map[uint64]bool, len(matched))
		for _, id := range matched {
			result[id] = true
		}
		return result, nil
	}
	following, err = load("followed_tags")
	if err == nil {
		featuring, err = load("featured_tags")
	}
	return following, featuring, err
}
