package mediaproc

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func TestRenewKeepsAnActiveEncodeFromBeingClaimed(t *testing.T) {
	conn := workerDB(t)
	_ = seedQueued(t, conn)
	start := time.Now()
	task, ok, err := Claim(context.Background(), conn, start)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, Renew(context.Background(), conn, task), "the owner must be able to extend the lease")
	_, ok, err = Claim(context.Background(), conn, start.Add(leaseFor))
	require.NoError(t, err)
	require.False(t, ok, "a renewed lease is not stolen after the original deadline")
}

func TestReclaimCancelsTheOldEncodeAndLeavesTheNewOutput(t *testing.T) {
	conn := workerDB(t)
	mem := storagetest.NewMemory()
	restore := storage.SetDefault(mem)
	t.Cleanup(restore)
	row := seedQueued(t, conn)
	task, ok, err := Claim(context.Background(), conn, time.Now())
	require.NoError(t, err)
	require.True(t, ok)
	started := make(chan struct{})
	prev := processFile
	processFile = func(ctx context.Context, _, _, _ string, _ systemsettings.MediaProcessing, _ Limits) (Output, error) {
		close(started)
		<-ctx.Done()
		return Output{}, ctx.Err()
	}
	t.Cleanup(func() { processFile = prev })
	finished := make(chan struct{})
	go func() {
		_ = runClaimed(context.Background(), conn, task)
		close(finished)
	}()
	<-started
	require.NoError(t, conn.Model(&models.MediaProcessTask{}).Where("id = ?", task.ID).Update("claim_expires_at", time.Now().Add(-time.Second)).Error)
	next, ok, err := Claim(context.Background(), conn, time.Now())
	require.NoError(t, err)
	require.True(t, ok)
	require.NotEqual(t, task.OutputKey, next.OutputKey)
	require.NotEqual(t, task.ClaimToken, next.ClaimToken)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("the old encode was still running when the new claim returned")
	}
	require.NoError(t, mem.Put(context.Background(), next.OutputKey, bytes.NewReader([]byte("new-output")), 10, "video/webm"))
	out := filepath.Join(t.TempDir(), "stale.webm")
	require.NoError(t, os.WriteFile(out, []byte("stale"), 0o600))
	require.NoError(t, publish(context.Background(), conn, task, Output{Path: out, MIME: "video/webm", OutputBytes: 5}))
	rc, _, err := mem.Open(context.Background(), next.OutputKey)
	require.NoError(t, err)
	_ = rc.Close()
	var again models.MediaAttachment
	require.NoError(t, conn.First(&again, row.Id).Error)
	require.NotEqual(t, models.ProcessReady, again.ProcessState)
}

func TestExpiredSavingIsRecoveredAndMissingStagingFreesTheQueue(t *testing.T) {
	conn := workerDB(t)
	for range maxVideoGlobal {
		row := &models.MediaAttachment{AccountId: 3, FileName: "processing", ContentType: "video/mp4", Type: models.MediaTypeUnknown, ProcessState: models.ProcessSaving}
		require.NoError(t, conn.Create(row).Error)
		expired := time.Now().Add(-time.Minute)
		task := models.MediaProcessTask{MediaID: row.Id, AccountID: 3, Kind: "video", DetectedMIME: "video/mp4", State: models.ProcessSaving, StagingPath: filepath.Join(t.TempDir(), "gone"), OutputKey: "media/old/out.webm", ClaimExpiresAt: &expired, ConfigJSON: `{}`}
		require.NoError(t, conn.Create(&task).Error)
	}
	dir := t.TempDir()
	staging := filepath.Join(dir, "input")
	require.NoError(t, os.WriteFile(staging, []byte("x"), 0o600))
	fresh := &models.MediaAttachment{AccountId: 9, FileName: "processing", ContentType: "video/mp4", Type: models.MediaTypeUnknown, ProcessState: models.ProcessQueued}
	require.NoError(t, conn.Create(fresh).Error)
	err := Enqueue(context.Background(), conn, fresh.Id, fresh.AccountId, "video", "video/mp4", staging, "media/new/out.webm", 1)
	require.Error(t, err)
	prev := processFile
	processFile = func(context.Context, string, string, string, systemsettings.MediaProcessing, Limits) (Output, error) {
		t.Fatal("missing staging must not start ffmpeg")
		return Output{}, nil
	}
	t.Cleanup(func() { processFile = prev })
	for range maxVideoGlobal {
		_ = ProcessNext(context.Background(), conn)
	}
	var failed int64
	require.NoError(t, conn.Model(&models.MediaProcessTask{}).Where("state = ?", models.ProcessFailed).Count(&failed).Error)
	require.EqualValues(t, maxVideoGlobal, failed)
	err = Enqueue(context.Background(), conn, fresh.Id, fresh.AccountId, "video", "video/mp4", staging, "media/new/out.webm", 1)
	require.NoError(t, err)
}

func TestEncodeSlotRunsOneAndWaits(t *testing.T) {
	require.NoError(t, acquireEncode(context.Background()))
	wait, stop := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer stop()
	require.ErrorIs(t, acquireEncode(wait), context.DeadlineExceeded)
	releaseEncode()
	require.NoError(t, acquireEncode(context.Background()))
	encodeMu.Lock()
	running := encodeRunning
	encodeMu.Unlock()
	require.Equal(t, 1, running)
	releaseEncode()
	encodeMu.Lock()
	running = encodeRunning
	encodeMu.Unlock()
	require.Equal(t, 0, running)
}

func TestFailedEncodeDeletesItsObjectAndMarksTheRowFailed(t *testing.T) {
	conn := workerDB(t)
	mem := storagetest.NewMemory()
	restore := storage.SetDefault(mem)
	t.Cleanup(restore)
	row := seedQueued(t, conn)
	task, ok, err := Claim(context.Background(), conn, time.Now())
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, mem.Put(context.Background(), task.OutputKey, bytes.NewReader([]byte("partial")), 7, "video/webm"))
	prev := processFile
	processFile = func(context.Context, string, string, string, systemsettings.MediaProcessing, Limits) (Output, error) {
		return Output{}, ErrProcess
	}
	t.Cleanup(func() { processFile = prev })
	require.Error(t, runClaimed(context.Background(), conn, task))
	_, _, err = mem.Open(context.Background(), task.OutputKey)
	require.Error(t, err)
	var again models.MediaAttachment
	require.NoError(t, conn.First(&again, row.Id).Error)
	require.Equal(t, models.ProcessFailed, again.ProcessState)
}

func TestSweepStagingRunsAgainAfterTheFirstPass(t *testing.T) {
	t.Chdir(t.TempDir())
	plant := func(name string) string {
		dir := filepath.Join("uploads", ".private-staging", name)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		old := time.Now().Add(-48 * time.Hour)
		require.NoError(t, os.Chtimes(dir, old, old))
		return dir
	}
	first := plant("old")
	sweepStaging(sweepBatch)
	_, err := os.Stat(first)
	require.True(t, os.IsNotExist(err))
	second := plant("later")
	sweepStaging(sweepBatch)
	_, err = os.Stat(second)
	require.True(t, os.IsNotExist(err))
}

func TestFencedProgressIsPersistedBeforePublish(t *testing.T) {
	conn := workerDB(t)
	restore := storage.SetDefault(storagetest.NewMemory())
	t.Cleanup(restore)
	_ = seedQueued(t, conn)
	task, ok, err := Claim(context.Background(), conn, time.Now())
	require.NoError(t, err)
	require.True(t, ok)
	prevFile, prevPreview := processFile, preview
	release := make(chan struct{})
	seen := make(chan struct{})
	again := make(chan struct{})
	processFile = func(ctx context.Context, _, work, _ string, _ systemsettings.MediaProcessing, _ Limits) (Output, error) {
		progressFrom(ctx)(55)
		close(seen)
		<-again
		progressFrom(ctx)(80)
		<-release
		out := filepath.Join(work, "output.webm")
		require.NoError(t, os.WriteFile(out, []byte("webm"), 0o600))
		return Output{Path: out, MIME: "video/webm", OutputBytes: 4, Info: Probe{Width: 2, Height: 2}}, nil
	}
	preview = func(context.Context, string, string, string) (string, string, int, int, error) {
		return "", "", 2, 2, nil
	}
	t.Cleanup(func() { processFile, preview = prevFile, prevPreview })
	done := make(chan error, 1)
	go func() { done <- runClaimed(context.Background(), conn, task) }()
	<-seen
	var mid models.MediaProcessTask
	require.NoError(t, conn.First(&mid, task.ID).Error)
	require.Equal(t, 55, mid.Progress)
	require.NoError(t, conn.Model(&models.MediaProcessTask{}).Where("id = ?", task.ID).Update("claim_token", "stolen").Error)
	close(again)
	time.Sleep(20 * time.Millisecond)
	var stolen models.MediaProcessTask
	require.NoError(t, conn.First(&stolen, task.ID).Error)
	require.Equal(t, 55, stolen.Progress, "a stale owner must not overwrite progress")
	require.NoError(t, conn.Model(&models.MediaProcessTask{}).Where("id = ?", task.ID).Update("claim_token", task.ClaimToken).Error)
	close(release)
	require.NoError(t, <-done)
}
