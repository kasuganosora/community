// Package transfer exposes separate administrator and account backup APIs.
package transfer

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/controllers"
	data "github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

func principal(c *gin.Context, scope string) (*models.User, *models.Account, bool) {
	account := controllers.GetAccount(c)
	if account == nil || account.UserId == 0 {
		c.AbortWithStatusJSON(401, gin.H{"error": "authentication required"})
		return nil, nil, false
	}
	var user models.User
	if db.GetDB().WithContext(c.Request.Context()).First(&user, account.UserId).Error != nil || user.IsBan || user.Status != models.UserStatusNormal {
		c.AbortWithStatusJSON(403, gin.H{"error": "account is not permitted to transfer data"})
		return nil, nil, false
	}
	if scope == "site" && !user.IsAdmin {
		c.AbortWithStatusJSON(403, gin.H{"error": "administrator access required"})
		return nil, nil, false
	}
	return &user, account, true
}

func ownedJob(c *gin.Context, scope string) (*data.Job, bool) {
	u, a, ok := principal(c, scope)
	if !ok {
		return nil, false
	}
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		c.JSON(404, gin.H{"error": "import job not found"})
		return nil, false
	}
	var j data.Job
	err := db.GetDB().WithContext(c.Request.Context()).Where("id = ? AND owner_user_id = ? AND owner_account_id = ? AND scope = ?", id, u.Id, a.Id, scope).First(&j).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "import job not found"})
		return nil, false
	}
	return &j, true
}

func Create(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		u, a, ok := principal(c, scope)
		if !ok {
			return
		}
		var open int64
		conn := db.GetDB().WithContext(c.Request.Context())
		if err := conn.Model(&data.Job{}).Where("owner_user_id = ? AND state IN ?", u.Id, []string{"validating", "ready", "running", "failed", "blocked"}).Count(&open).Error; err != nil {
			c.JSON(500, gin.H{"error": "could not inspect import jobs"})
			return
		}
		if open >= 5 {
			c.JSON(429, gin.H{"error": "finish or delete existing import jobs before uploading another backup"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, data.MaxUploadBytes+(1<<20))
		if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
			c.JSON(413, gin.H{"error": "invalid multipart backup or upload larger than 256 MiB"})
			return
		}
		if c.Request.MultipartForm != nil {
			defer c.Request.MultipartForm.RemoveAll()
		}
		files := c.Request.MultipartForm.File["file"]
		if len(files) != 1 || len(c.Request.MultipartForm.File) != 1 {
			c.JSON(422, gin.H{"error": "upload exactly one backup file"})
			return
		}
		f, err := files[0].Open()
		if err != nil {
			c.JSON(422, gin.H{"error": "backup file is not readable"})
			return
		}
		defer f.Close()
		mediaRoot, err := filepath.Abs(filepath.Join("uploads", "media"))
		if err != nil {
			c.JSON(500, gin.H{"error": "media storage unavailable"})
			return
		}
		j, err := data.StageJob(c.Request.Context(), db.GetDB(), f, files[0].Filename, c.PostForm("source"), c.PostForm("kind"), scope, u.Id, a.Id, systemsettings.GetBaseURL(), mediaRoot)
		if err != nil {
			c.JSON(422, gin.H{"error": "backup could not be staged; check its format, size and available storage"})
			return
		}
		data.StartJob(db.GetDB(), j.ID)
		c.JSON(http.StatusAccepted, j)
	}
}

func List(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		u, a, ok := principal(c, scope)
		if !ok {
			return
		}
		jobs := make([]data.Job, 0)
		if err := db.GetDB().WithContext(c.Request.Context()).Where("owner_user_id = ? AND owner_account_id = ? AND scope = ?", u.Id, a.Id, scope).Order("created_at DESC").Limit(50).Find(&jobs).Error; err != nil {
			c.JSON(500, gin.H{"error": "could not list import jobs"})
			return
		}
		c.JSON(200, jobs)
	}
}

func Get(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		j, ok := ownedJob(c, scope)
		if ok {
			c.JSON(200, j)
		}
	}
}

func Execute(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		j, ok := ownedJob(c, scope)
		if !ok {
			return
		}
		var request struct {
			Revision string `json:"revision"`
		}
		decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			c.JSON(422, gin.H{"error": "a preview revision is required"})
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			c.JSON(422, gin.H{"error": "one JSON object required"})
			return
		}
		if err := data.ExecuteJob(c.Request.Context(), db.GetDB(), j, request.Revision); err != nil {
			c.JSON(409, gin.H{"error": "job is not executable at this revision; reload the preflight report"})
			return
		}
		data.StartJob(db.GetDB(), j.ID)
		if err := db.GetDB().WithContext(c.Request.Context()).First(j, "id = ?", j.ID).Error; err != nil {
			c.JSON(500, gin.H{"error": "could not read import status"})
			return
		}
		c.JSON(http.StatusAccepted, j)
	}
}

func Report(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		j, ok := ownedJob(c, scope)
		if !ok {
			return
		}
		if j.Report == nil {
			c.JSON(409, gin.H{"error": "report is not ready"})
			return
		}
		c.Header("Content-Disposition", `attachment; filename="migration-report-`+j.ID+`.json"`)
		c.Header("Cache-Control", "no-store")
		c.JSON(200, j.Report)
	}
}

func Delete(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		j, ok := ownedJob(c, scope)
		if !ok {
			return
		}
		if j.State == "running" || j.State == "validating" {
			c.JSON(409, gin.H{"error": "wait until the job stops before deleting its package"})
			return
		}
		root, err := data.TransferRoot()
		if err != nil {
			c.JSON(500, gin.H{"error": "private backup storage unavailable"})
			return
		}
		if filepath.Clean(j.Directory) != filepath.Join(root, j.ID) {
			c.JSON(500, gin.H{"error": "private backup location changed"})
			return
		}
		result := db.GetDB().WithContext(c.Request.Context()).Where("id = ? AND owner_user_id = ? AND owner_account_id = ? AND scope = ? AND state NOT IN ?", j.ID, j.OwnerUserID, j.OwnerAccountID, scope, []string{"running", "validating"}).Delete(&data.Job{})
		if result.Error != nil {
			c.JSON(500, gin.H{"error": "could not delete import job"})
			return
		}
		if result.RowsAffected == 0 {
			c.JSON(409, gin.H{"error": "import job is active"})
			return
		}
		// Receipts and imported objects deliberately remain: removing an uploaded
		// package is not an undo, and must not make a later upload duplicate data.
		if err = os.RemoveAll(j.Directory); err != nil {
			c.JSON(500, gin.H{"error": "job removed but its private package could not be cleaned"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func Export(c *gin.Context) {
	_, a, ok := principal(c, "personal")
	if !ok {
		return
	}
	root, err := data.TransferRoot()
	if err != nil {
		c.JSON(500, gin.H{"error": "private export storage unavailable"})
		return
	}
	f, err := os.CreateTemp(root, "personal-export-*.zip")
	if err != nil {
		c.JSON(500, gin.H{"error": "could not prepare export"})
		return
	}
	name := f.Name()
	defer os.Remove(name)
	if err = data.ExportPersonal(c.Request.Context(), db.GetDB(), a.Id, systemsettings.GetBaseURL(), f); err != nil {
		_ = f.Close()
		c.JSON(422, gin.H{"error": "backup could not be completed; check required media and storage"})
		return
	}
	if err = f.Close(); err != nil {
		c.JSON(500, gin.H{"error": "could not finish backup"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.FileAttachment(name, "community-backup-"+time.Now().UTC().Format("2006-01-02")+".zip")
}

// AuthorizeTransfer accepts web sessions or full read/write OAuth scopes, never
// a profile-only third-party token that could otherwise expose private posts.
func AuthorizeTransfer(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	raw, exists := c.Get("token_data")
	td, ok := raw.(*models.AuthTokenData)
	if !exists || !ok || td == nil {
		c.AbortWithStatusJSON(401, gin.H{"error": "authentication required"})
		return
	}
	if td.AuthType == models.AuthTokenTypeWeb {
		c.Next()
		return
	}
	readOnly := c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead
	typeID, value := uint8(models.AppScopeTypeWrite), uint64(models.AppScopeWrite)
	if readOnly {
		typeID, value = models.AppScopeTypeRead, models.AppScopeRead
	}
	if td.Scopes == nil || !td.Scopes.HasScope(typeID, value) {
		c.AbortWithStatusJSON(403, gin.H{"error": "full account data scope required"})
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/") && !td.Scopes.HasScope(models.AppScopeTypeAdmin, models.AppScopeAdminWrite) {
		c.AbortWithStatusJSON(403, gin.H{"error": "administrator scope required"})
		return
	}
	c.Next()
}
