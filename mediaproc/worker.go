package mediaproc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const (
	maxVideoPerUser = 2
	maxVideoGlobal  = 8
	leaseFor        = 2 * time.Minute
	stagingMaxAge   = 24 * time.Hour
)

type frozenConfig struct {
	systemsettings.MediaProcessing
	Limits Limits `json:"limits"`
}

// Enqueue freezes the current settings onto a task. The attachment row must
// already exist in the queued state. Video tasks are counted against the queue.
func Enqueue(ctx context.Context, conn *gorm.DB, mediaID, accountID uint64, kind, detected, stagingPath, outputKey string, inputBytes int64) error {
	if kind == "video" {
		var userN, globalN int64
		live := []string{models.ProcessQueued, models.ProcessProcessing, models.ProcessSaving}
		if err := conn.WithContext(ctx).Model(&models.MediaProcessTask{}).Where("kind = ? AND state IN ?", "video", live).Count(&globalN).Error; err != nil {
			return err
		}
		if globalN >= maxVideoGlobal {
			return errQueue
		}
		if err := conn.WithContext(ctx).Model(&models.MediaProcessTask{}).Where("kind = ? AND account_id = ? AND state IN ?", "video", accountID, live).Count(&userN).Error; err != nil {
			return err
		}
		if userN >= maxVideoPerUser {
			return errQueue
		}
	}
	snap := frozenConfig{MediaProcessing: systemsettings.LoadMediaProcessing(), Limits: DefaultLimits(systemsettings.MediaImageSizeLimit(), systemsettings.MediaVideoSizeLimit())}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	task := models.MediaProcessTask{MediaID: mediaID, AccountID: accountID, Kind: kind, DetectedMIME: detected, State: models.ProcessQueued, StagingPath: stagingPath, OutputKey: outputKey, ConfigJSON: string(raw), InputBytes: inputBytes}
	return conn.WithContext(ctx).Create(&task).Error
}

var errQueue = errString("upload queue is full")

type errString string

func (e errString) Error() string { return string(e) }

// Claim takes one queued task, or a processing task whose lease has expired.
// A live lease is not claimed again.
func Claim(ctx context.Context, conn *gorm.DB, now time.Time) (*models.MediaProcessTask, bool, error) {
	var claimed models.MediaProcessTask
	var ok bool
	err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task models.MediaProcessTask
		q := tx.Session(&gorm.Session{Logger: logger.Discard}).Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"state = ? OR (state = ? AND (claim_expires_at IS NULL OR claim_expires_at <= ?))",
			models.ProcessQueued, models.ProcessProcessing, now,
		).Order("id").Limit(1)
		if err := q.First(&task).Error; err != nil {
			return err
		}
		token := uuid.NewString()
		until := now.Add(leaseFor)
		res := tx.Model(&models.MediaProcessTask{}).Where(
			"id = ? AND (state = ? OR (state = ? AND (claim_expires_at IS NULL OR claim_expires_at <= ?)))",
			task.ID, models.ProcessQueued, models.ProcessProcessing, now,
		).Updates(map[string]any{"state": models.ProcessProcessing, "claim_token": token, "claim_expires_at": until})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		task.State = models.ProcessProcessing
		task.ClaimToken = token
		task.ClaimExpiresAt = &until
		claimed = task
		ok = true
		return tx.Model(&models.MediaAttachment{}).Where("id = ? AND process_state IN ?", task.MediaID, []string{models.ProcessQueued, models.ProcessProcessing}).Update("process_state", models.ProcessProcessing).Error
	})
	if err == gorm.ErrRecordNotFound {
		return nil, false, nil
	}
	return &claimed, ok, err
}

// Cancel marks an in-flight upload cancelled. A later finalize with the old
// claim token cannot publish it. Ready attachments are left for the normal delete.
func Cancel(ctx context.Context, conn *gorm.DB, mediaID, accountID uint64) (bool, error) {
	var task models.MediaProcessTask
	err := conn.WithContext(ctx).Where("media_id = ? AND account_id = ?", mediaID, accountID).First(&task).Error
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if task.State == models.ProcessReady || task.State == models.ProcessFailed || task.State == models.ProcessCancelled {
		return false, nil
	}
	err = conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.MediaProcessTask{}).Where("id = ? AND state IN ?", task.ID, []string{models.ProcessQueued, models.ProcessProcessing, models.ProcessSaving}).Updates(map[string]any{"state": models.ProcessCancelled, "claim_token": "", "error_text": "media processing was cancelled"}).Error; err != nil {
			return err
		}
		return tx.Model(&models.MediaAttachment{}).Where("id = ? AND account_id = ? AND process_state IN ?", mediaID, accountID, []string{models.ProcessQueued, models.ProcessProcessing, models.ProcessSaving}).Updates(map[string]any{"process_state": models.ProcessCancelled, "process_error": "media processing was cancelled"}).Error
	})
	if task.StagingPath != "" {
		_ = os.RemoveAll(filepath.Dir(task.StagingPath))
	}
	if task.OutputKey != "" {
		_ = storage.Default().Delete(ctx, task.OutputKey)
	}
	return true, err
}

// ProcessNext claims and finishes one task. It is safe to call from the
// request path and from the restart loop.
func ProcessNext(ctx context.Context, conn *gorm.DB) error {
	task, ok, err := Claim(ctx, conn, time.Now())
	if err != nil || !ok {
		return err
	}
	return runClaimed(ctx, conn, task)
}

func runClaimed(ctx context.Context, conn *gorm.DB, task *models.MediaProcessTask) error {
	var snap frozenConfig
	if json.Unmarshal([]byte(task.ConfigJSON), &snap) != nil {
		return failTask(ctx, conn, task, ErrProcess)
	}
	work := filepath.Dir(task.StagingPath)
	out, err := processFile(ctx, task.StagingPath, work, task.DetectedMIME, snap.MediaProcessing, snap.Limits)
	if err != nil {
		return failTask(ctx, conn, task, err)
	}
	return publish(ctx, conn, task, out)
}

func publish(ctx context.Context, conn *gorm.DB, task *models.MediaProcessTask, out Output) error {
	res := conn.WithContext(ctx).Model(&models.MediaProcessTask{}).Where("id = ? AND claim_token = ? AND state = ?", task.ID, task.ClaimToken, models.ProcessProcessing).Update("state", models.ProcessSaving)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		_ = os.RemoveAll(filepath.Dir(task.StagingPath))
		return nil
	}
	_ = conn.WithContext(ctx).Model(&models.MediaAttachment{}).Where("id = ? AND process_state = ?", task.MediaID, models.ProcessProcessing).Updates(map[string]any{"process_state": models.ProcessSaving, "process_progress": 100})
	if err := storage.PutFile(ctx, out.Path, task.OutputKey, out.MIME); err != nil {
		_ = storage.Default().Delete(ctx, task.OutputKey)
		return failTask(ctx, conn, task, ErrProcess)
	}
	thumbName, blur, w, h := "", "", out.Info.Width, out.Info.Height
	if preview != nil {
		name, bh, pw, ph, perr := preview(ctx, out.Path, out.MIME, filepath.Dir(task.StagingPath))
		if perr != nil {
			_ = storage.Default().Delete(ctx, task.OutputKey)
			return failTask(ctx, conn, task, ErrProcess)
		}
		thumbName, blur = name, bh
		if pw > 0 {
			w, h = pw, ph
		}
		if thumbName != "" {
			thumbKey := filepath.ToSlash(filepath.Join(filepath.Dir(task.OutputKey), thumbName))
			if err := storage.PutFile(ctx, filepath.Join(filepath.Dir(out.Path), thumbName), thumbKey, "image/jpeg"); err != nil {
				_ = storage.Default().Delete(ctx, task.OutputKey)
				_ = storage.Default().Delete(ctx, thumbKey)
				return failTask(ctx, conn, task, ErrProcess)
			}
		}
	}
	rel := strings.TrimPrefix(filepath.ToSlash(task.OutputKey), models.MediaKeyPrefix)
	fileName := path.Base(rel)
	var dur any
	if out.Info.Duration > 0 {
		dur = out.Info.Duration
	}
	committed := false
	err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.MediaProcessTask{}).Where("id = ? AND claim_token = ? AND state = ?", task.ID, task.ClaimToken, models.ProcessSaving).Updates(map[string]any{
			"state": models.ProcessReady, "progress": 100, "claim_token": "", "output_bytes": out.OutputBytes, "error_text": "",
		})
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error
		}
		updates := map[string]any{
			"file_name": fileName, "file_path": rel, "file_size": out.OutputBytes, "content_type": out.MIME,
			"type": string(models.GetMediaType(out.MIME)), "process_state": models.ProcessReady, "process_progress": 100, "process_error": "",
			"blurhash": blur,
		}
		if w > 0 {
			updates["width"] = w
			updates["height"] = h
		}
		if dur != nil {
			updates["duration"] = dur
		}
		if thumbName != "" {
			updates["thumbnail_path"] = thumbName
		}
		res = tx.Model(&models.MediaAttachment{}).Where("id = ? AND process_state = ?", task.MediaID, models.ProcessSaving).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		committed = true
		return nil
	})
	if !committed {
		_ = storage.Default().Delete(ctx, task.OutputKey)
		if thumbName != "" {
			_ = storage.Default().Delete(ctx, filepath.ToSlash(filepath.Join(filepath.Dir(task.OutputKey), thumbName)))
		}
		_ = os.RemoveAll(filepath.Dir(task.StagingPath))
		return nil
	}
	_ = os.RemoveAll(filepath.Dir(task.StagingPath))
	log.Info("media %d processed kind=%s in=%d out=%d", task.MediaID, task.Kind, task.InputBytes, out.OutputBytes)
	return err
}

func failTask(ctx context.Context, conn *gorm.DB, task *models.MediaProcessTask, cause error) error {
	msg := publicMessage(cause)
	_ = conn.WithContext(ctx).Model(&models.MediaProcessTask{}).Where("id = ? AND claim_token = ?", task.ID, task.ClaimToken).Updates(map[string]any{"state": models.ProcessFailed, "error_text": msg, "claim_token": ""})
	_ = conn.WithContext(ctx).Model(&models.MediaAttachment{}).Where("id = ? AND process_state IN ?", task.MediaID, []string{models.ProcessQueued, models.ProcessProcessing, models.ProcessSaving}).Updates(map[string]any{"process_state": models.ProcessFailed, "process_error": msg})
	if task.StagingPath != "" {
		_ = os.RemoveAll(filepath.Dir(task.StagingPath))
	}
	log.Info("media %d processing rejected kind=%s", task.MediaID, task.Kind)
	return cause
}

func publicMessage(err error) string {
	switch {
	case errors.Is(err, ErrCorrupt):
		return ErrCorrupt.Error()
	case errors.Is(err, ErrPixels):
		return ErrPixels.Error()
	case errors.Is(err, ErrDuration):
		return ErrDuration.Error()
	case errors.Is(err, ErrRate):
		return ErrRate.Error()
	case errors.Is(err, ErrTooLarge):
		return ErrTooLarge.Error()
	case err != nil && err.Error() == "unsupported media type":
		return err.Error()
	default:
		return ErrProcess.Error()
	}
}

// Start resumes expired leases and orphan staging until ctx is cancelled.
// One video is encoded at a time.
func Start(ctx context.Context, conn *gorm.DB) {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		sweepStaging()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := ProcessNext(ctx, conn); err != nil && ctx.Err() == nil {
					log.Warn("media processing: %v", err)
				}
			}
		}
	}()
}

func sweepStaging() {
	root := filepath.Join("uploads", ".private-staging")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-stagingMaxAge)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, entry.Name()))
	}
}

// preview builds the JPEG poster. Tests replace it. A nil preview skips the poster.
var preview = defaultPreview

// processFile is ProcessFile except in tests that substitute a fake encoder.
var processFile = ProcessFile
