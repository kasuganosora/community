package filter

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The v1 API addresses an individual keyword. V2 groups several keywords
// under one filter, so a v1 ID includes the filter and its stable keyword ID.
type legacyFilter struct {
	Id           string              `json:"id"`
	Phrase       string              `json:"phrase"`
	Context      []string            `json:"context"`
	WholeWord    bool                `json:"whole_word"`
	ExpiresAt    *serializer.APITime `json:"expires_at"`
	Irreversible bool                `json:"irreversible"`
}

func toLegacyFilter(filter *models.Filter, keyword *models.FilterKeyword) legacyFilter {
	context := filter.Context
	if context == nil {
		context = []string{}
	}
	return legacyFilter{
		Id: strconv.FormatUint(filter.Id, 10) + "-" + keyword.KeyID(), Phrase: keyword.Keyword,
		Context: context, WholeWord: keyword.WholeWord, ExpiresAt: serializer.NewAPITime(filter.ExpiresAt),
		Irreversible: filter.FilterAction == models.FilterActionHide,
	}
}

func LegacyList(c *gin.Context) {
	var filters []*models.Filter
	conn := db.GetDB().WithContext(c.Request.Context())
	if err := conn.Where("account_id = ?", controllers.MustGetAccount(c).Id).Order("id ASC").Find(&filters).Error; err != nil {
		_ = c.Error(err)
		return
	}
	result := make([]legacyFilter, 0)
	for _, filter := range filters {
		for _, keyword := range filter.Keywords {
			if keyword != nil {
				result = append(result, toLegacyFilter(filter, keyword))
			}
		}
	}
	c.JSON(http.StatusOK, result)
}

type legacyFilterRequest struct {
	Phrase       *string   `json:"phrase"`
	Context      *[]string `json:"context"`
	ExpiresIn    *int64    `json:"expires_in" binding:"omitempty,gte=0,lte=9223372036"`
	Irreversible *bool     `json:"irreversible"`
	WholeWord    *bool     `json:"whole_word"`
}

func LegacyCreate(c *gin.Context) {
	mutateLegacyFilter(c, true)
}

func LegacyUpdate(c *gin.Context) {
	mutateLegacyFilter(c, false)
}

func mutateLegacyFilter(c *gin.Context, creating bool) {
	var req legacyFilterRequest
	if err := controllers.BindBody(c, &req); err != nil {
		fail(c, 422, ErrCodeFilterInvalidRequest, err.Error())
		return
	}
	if creating && req.Phrase == nil {
		fail(c, 422, ErrCodeFilterKeywordEmpty, "Keyword cannot be empty")
		return
	}
	if (creating && req.Context == nil) || (req.Context != nil && !models.ValidFilterContexts(*req.Context)) {
		fail(c, 422, ErrCodeFilterContextUnsupported, "Unsupported filter context")
		return
	}
	if req.Phrase != nil {
		phrase, err := validKeyword(*req.Phrase)
		if err != nil {
			fail(c, err.status, err.code, err.msg)
			return
		}
		req.Phrase = &phrase
	}

	accountID := controllers.MustGetAccount(c).Id
	var reply legacyFilter
	err := db.GetDB().WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		filter := &models.Filter{AccountId: accountID, FilterAction: models.FilterActionWarn}
		keyword := &models.FilterKeyword{Uuid: uuid.NewString(), WholeWord: true}
		if creating {
			filter.Keywords = []*models.FilterKeyword{keyword}
		} else {
			var err error
			filter, keyword, err = findLegacyKeyword(tx.Clauses(clause.Locking{Strength: "UPDATE"}), accountID, c.Param("id"))
			if err != nil {
				return err
			}
		}
		if len(filter.Keywords) > 1 {
			changesContext := req.Context != nil && !slices.Equal(*req.Context, filter.Context)
			changesExpiry := req.ExpiresIn != nil && (*req.ExpiresIn > 0 || filter.ExpiresAt != nil)
			changesAction := req.Irreversible != nil && *req.Irreversible != (filter.FilterAction == models.FilterActionHide)
			if changesContext || changesExpiry || changesAction {
				return errLegacyMultipleKeywords
			}
		}
		if req.Phrase != nil {
			keyword.Uuid = keyword.KeyID()
			keyword.Keyword = *req.Phrase
			if creating || len(filter.Keywords) == 1 {
				filter.Title = *req.Phrase
			}
		}
		if req.Context != nil {
			filter.Context = *req.Context
		}
		if req.ExpiresIn != nil {
			filter.ExpiresAt = nil
			if *req.ExpiresIn > 0 {
				expires := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second)
				filter.ExpiresAt = &expires
			}
		}
		if req.Irreversible != nil {
			filter.FilterAction = models.FilterActionWarn
			if *req.Irreversible {
				filter.FilterAction = models.FilterActionHide
			}
		}
		if req.WholeWord != nil {
			keyword.WholeWord = *req.WholeWord
		}
		if err := tx.Save(filter).Error; err != nil {
			return err
		}
		reply = toLegacyFilter(filter, keyword)
		return nil
	})
	if err != nil {
		legacyFilterError(c, err)
		return
	}
	c.JSON(http.StatusOK, reply)
}

func LegacyShow(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context())
	filter, keyword, err := findLegacyKeyword(conn, controllers.MustGetAccount(c).Id, c.Param("id"))
	if err != nil {
		legacyFilterError(c, err)
		return
	}
	c.JSON(http.StatusOK, toLegacyFilter(filter, keyword))
}

func LegacyDelete(c *gin.Context) {
	err := db.GetDB().WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		filter, keyword, err := findLegacyKeyword(tx.Clauses(clause.Locking{Strength: "UPDATE"}), controllers.MustGetAccount(c).Id, c.Param("id"))
		if err != nil {
			return err
		}
		for i, candidate := range filter.Keywords {
			if candidate == keyword {
				filter.Keywords = append(filter.Keywords[:i], filter.Keywords[i+1:]...)
				break
			}
		}
		// A v1 entry represents only its keyword. The v2 filter can still be
		// referenced by another client even when it has no remaining keywords.
		return tx.Select("Keywords").Save(filter).Error
	})
	if err != nil {
		legacyFilterError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{})
}

func findLegacyKeyword(conn *gorm.DB, accountID uint64, id string) (*models.Filter, *models.FilterKeyword, error) {
	rawFilterID, keywordID, hasKeywordID := strings.Cut(id, "-")
	filterID, err := strconv.ParseUint(rawFilterID, 10, 64)
	if err != nil || filterID == 0 {
		return nil, nil, gorm.ErrRecordNotFound
	}
	filter := &models.Filter{}
	if err := conn.Where("id = ? AND account_id = ?", filterID, accountID).First(filter).Error; err != nil {
		return nil, nil, err
	}
	for _, keyword := range filter.Keywords {
		if keyword != nil && (keyword.KeyID() == keywordID || !hasKeywordID && len(filter.Keywords) == 1) {
			return filter, keyword, nil
		}
	}
	return nil, nil, gorm.ErrRecordNotFound
}

var errLegacyMultipleKeywords = errors.New("Use the v2 API to change settings on a filter with multiple keywords")

func legacyFilterError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		fail(c, 404, ErrCodeFilterKeywordNotFound, "Filter keyword not found")
	case errors.Is(err, errLegacyMultipleKeywords):
		fail(c, 422, ErrCodeFilterInvalidRequest, err.Error())
	default:
		_ = c.Error(err)
	}
}
