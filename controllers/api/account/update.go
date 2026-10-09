package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/cache"
	mediactl "github.com/kasuganosora/community/controllers/api/media"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/monoculum/formam/v3"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"mime/multipart"
	"net/http"
	"strings"
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

		// 验证文件内容为图片（防止上传非图片文件作为头像）
		if err = validateImageFileHeader(avatar[0]); err != nil {
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

		// 验证文件内容为图片
		if err = validateImageFileHeader(header[0]); err != nil {
			return err
		}

		req.Header = header[0]
	}

	return err
}

// validateImageFileHeader 验证上传的文件是否为图片（基于 magic bytes）.
// 头像和头图只允许图片类型，防止上传 HTML/SVG/可执行文件等非图片内容.
func validateImageFileHeader(fh *multipart.FileHeader) error {
	f, err := fh.Open()
	if err != nil {
		return errors.Wrap(err, "failed to open uploaded file for validation")
	}
	defer func() { _ = f.Close() }()

	// 读取前 512 字节用于类型检测
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err.Error() != "EOF" {
		return errors.Wrap(err, "failed to read file for validation")
	}

	contentType := http.DetectContentType(buf[:n])
	if !strings.HasPrefix(contentType, "image/") {
		return errors.Errorf("invalid file type: avatar/header must be an image (got %s)", contentType)
	}

	return nil
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
		imageURL, err := mediactl.StoreAccountImage(c.Request.Context(), account.Id, req.Avatar)
		if err != nil {
			c.JSON(500, gin.H{"error": "upload avatar failed"})

			err = errors.Wrap(err, "UpdateAccount")
			_ = c.Error(err)

			return
		}

		account.Avatar = imageURL

		updateFields = append(updateFields, "avatar")
	}

	// 如果有头图，就更新头图
	if req.Header != nil {
		imageURL, err := mediactl.StoreAccountImage(c.Request.Context(), account.Id, req.Header)
		if err != nil {
			c.JSON(500, gin.H{"error": "upload header failed"})

			err = errors.Wrap(err, "UpdateAccount")
			_ = c.Error(err)

			return
		}

		account.Header = imageURL

		updateFields = append(updateFields, "header")
	}

	// 更新其他信息
	if req.DisplayName != nil {
		account.DisplayName = *req.DisplayName

		updateFields = append(updateFields, "display_name")
	}

	if req.Note != nil {
		account.Note = *req.Note
		account.ImportedNoteHTML = ""

		updateFields = append(updateFields, "note", "imported_note_html")
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
		if err == nil {
			err = models.QueueFederationProfile(tx, account.Id)
		}

		return
	})
	if err != nil {
		c.JSON(500, gin.H{"error": "internal error"})

		err = errors.Wrap(err, "UpdateAccount")
		_ = c.Error(err)

		return
	}

	_ = cache.InvalidateAccount(c.Request.Context(), account.Id)
	acc := serializer.ToAccount(account)

	c.JSON(200, gin.H{"account": acc}) //nolint:goconst
}
