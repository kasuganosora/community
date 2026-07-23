package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/storage"
	"github.com/monoculum/formam/v3"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"mime/multipart"
)

type UpdateAccountRequest struct {
	DisplayName     *string `json:"display_name"`
	Note            *string `json:"note"`
	Header          *multipart.FileHeader
	Avatar          *multipart.FileHeader
	Locked          *bool                 `json:"locked"`
	Bot             *bool                 `json:"bot"`
	Discoverable    *bool                 `json:"discoverable"`
	HideCollections *bool                 `json:"hide_collections"`
	Fields          []models.AccountField `json:"fields_attributes"`
}

func (req *UpdateAccountRequest) Bind(c *gin.Context) (err error) {
	form, err := c.MultipartForm()
	if err != nil {
		return err
	}

	dec := formam.NewDecoder(&formam.DecoderOptions{TagName: "json"})

	err = dec.Decode(form.Value, req)
	if err != nil {
		return err
	}

	// 处理头像和头图
	if avatar, ok := form.File["avatar"]; ok {
		// 不能超过1M
		if avatar[0].Size > 1024*1024 {
			err = errors.New("avatar size too large")

			return err
		}

		req.Avatar = avatar[0]
	}

	if header, ok := form.File["header"]; ok {
		// 不能超过3M
		if header[0].Size > 1024*1024*3 {
			err = errors.New("header size too large")

			return err
		}

		req.Header = header[0]
	}

	return err
}

func UpdateAccount(c *gin.Context) {
	req := &UpdateAccountRequest{}
	if err := req.Bind(c); err != nil {
		c.JSON(422, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	// 获取当前用户
	user := c.MustGet("user").(*models.User) //nolint:forcetypeassert

	account := &models.Account{}

	err := db.GetDB().
		Preload("Extra").
		Preload("Emojis").
		Where("user_id = ?", user.Id).First(account).Error
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"}) //nolint:goconst

		err = errors.Wrap(err, "UpdateAccount")
		_ = c.Error(err)

		return
	}

	updateFields := make([]string, 0)

	// 如果有头像，就更新头像
	if req.Avatar != nil {
		saveFilePath := "public/avatars/" + user.UUID

		saveFilePath, err = storage.SaveByMultipart(saveFilePath, req.Avatar)
		if err != nil {
			c.JSON(500, gin.H{"error": "upload avatar failed"})

			err = errors.Wrap(err, "UpdateAccount")
			_ = c.Error(err)

			return
		}

		account.Avatar = saveFilePath

		updateFields = append(updateFields, "avatar")
	}

	// 如果有头图，就更新头图
	if req.Header != nil {
		saveFilePath := "public/headers/" + user.UUID

		saveFilePath, err = storage.SaveByMultipart(saveFilePath, req.Header)
		if err != nil {
			c.JSON(500, gin.H{"error": "upload header failed"})

			err = errors.Wrap(err, "UpdateAccount")
			_ = c.Error(err)

			return
		}

		account.Header = saveFilePath

		updateFields = append(updateFields, "header")
	}

	// 更新其他信息
	if req.DisplayName != nil {
		account.DisplayName = *req.DisplayName

		updateFields = append(updateFields, "display_name")
	}

	if req.Note != nil {
		account.Note = *req.Note

		updateFields = append(updateFields, "note")
	}

	if req.Locked != nil {
		account.Locked = *req.Locked

		updateFields = append(updateFields, "locked")
	}

	if req.Bot != nil {
		account.Bot = *req.Bot

		updateFields = append(updateFields, "bot")
	}

	if req.Discoverable != nil {
		account.Discoverable = *req.Discoverable

		updateFields = append(updateFields, "discoverable")
	}

	if req.HideCollections != nil {
		account.HideCollections = *req.HideCollections

		updateFields = append(updateFields, "hide_collections")
	}

	err = db.GetDB().Transaction(func(tx *gorm.DB) (err error) {
		if account.Extra == nil {
			account.Extra = &models.AccountExtra{}
			account.Extra.AccountId = account.Id
		}

		if req.Fields != nil {
			account.Extra.Fields = req.Fields

			err = tx.Save(account.Extra).Error
			if err != nil {
				return
			}
		}

		err = tx.Select(updateFields).Save(account).Error

		return
	})
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})

		err = errors.Wrap(err, "UpdateAccount")
		_ = c.Error(err)

		return
	}

	acc := serializer.ToAccount(account)

	c.JSON(200, gin.H{"account": acc}) //nolint:goconst
}
