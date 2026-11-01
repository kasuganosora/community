package status

import (
	"context"
	"errors"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/newacctmod"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNewAccountPostAndReviewCommitTogether(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "queue failure"}[fail], func(t *testing.T) {
			conn, _, owner := replacementDB(t)
			require.NoError(t, conn.AutoMigrate(newacctmod.Models()...))
			previous := newacctmod.Enabled()
			systemsettings.Set(newacctmod.KeyEnabled, true)
			t.Cleanup(func() { systemsettings.Set(newacctmod.KeyEnabled, previous) })
			if fail {
				require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("fail_review", func(tx *gorm.DB) {
					if tx.Statement.Table == "new_account_review_jobs" {
						tx.AddError(errors.New("queue unavailable"))
					}
				}))
				t.Cleanup(func() { conn.Callback().Create().Remove("fail_review") })
			}
			c, w := newGinContextPost(t, nil, CreateStatusRequest{Status: "review atomic post", Visibility: "public"})
			setAccount(c, owner)
			safeHandler(c, Create)
			var count int64
			require.NoError(t, conn.Model(&models.StatusContent{}).Where("content = ?", "review atomic post").Count(&count).Error)
			if fail {
				require.Equal(t, 500, w.Code, w.Body.String())
				require.Zero(t, count)
			} else {
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), `"local_only":true`)
				require.EqualValues(t, 1, count)
				var job newacctmod.ReviewJob
				require.NoError(t, conn.First(&job).Error)
				var post models.Status
				require.NoError(t, conn.First(&post, job.StatusID).Error)
				require.True(t, post.LocalOnly)
				require.Equal(t, owner.Id, job.AccountID)
			}
		})
	}
}

func TestReviewDecisionSurvivesObservationBoundary(t *testing.T) {
	conn, fix, owner := replacementDB(t)
	fix.CreateStatus(owner)
	require.NoError(t, conn.AutoMigrate(newacctmod.Models()...))
	previous := newacctmod.ObservationN()
	systemsettings.Set(newacctmod.KeyObservationN, 2) // fixture already has one post
	t.Cleanup(func() { systemsettings.Set(newacctmod.KeyObservationN, previous) })
	first := &models.Status{AccountId: owner.Id, Content: models.StatusContent{Content: "first concurrent post"}}
	second := &models.Status{AccountId: owner.Id, Content: models.StatusContent{Content: "second concurrent post"}}
	reviewFirst := newacctmod.PrepareCreate(conn, first, owner)
	reviewSecond := newacctmod.PrepareCreate(conn, second, owner)
	require.NotNil(t, reviewFirst)
	require.NotNil(t, reviewSecond)
	require.NoError(t, models.CreateStatusCommitted(conn, first, reviewFirst))
	require.NoError(t, models.CreateStatusCommitted(conn, second, reviewSecond))
	var jobs int64
	require.NoError(t, conn.Model(&newacctmod.ReviewJob{}).Count(&jobs).Error)
	require.EqualValues(t, 2, jobs, "both held posts must have a durable review")
}

func TestModeratorCanReleaseFlaggedReviewOnceAndInvalidateWorkerLease(t *testing.T) {
	conn, fix, owner := replacementDB(t)
	require.NoError(t, conn.AutoMigrate(newacctmod.Models()...))
	post := fix.CreateStatus(owner, func(s *models.Status) { s.LocalOnly = true })
	job := newacctmod.ReviewJob{StatusID: post.Id, AccountID: owner.Id, State: "done", Verdict: "flag", LeaseToken: "old-worker"}
	require.NoError(t, conn.Create(&job).Error)
	require.NoError(t, newacctmod.ReviewManually(context.Background(), conn, job.ID, owner.Id, "approve"))
	require.NoError(t, conn.First(&post, post.Id).Error)
	require.False(t, post.LocalOnly)
	require.NoError(t, conn.First(&job, job.ID).Error)
	require.Equal(t, "pass", job.Verdict)
	require.Empty(t, job.LeaseToken)
	require.ErrorIs(t, newacctmod.ReviewManually(context.Background(), conn, job.ID, owner.Id, "approve"), newacctmod.ErrReviewClosed)
	require.Error(t, newacctmod.ApplySuspend(context.Background(), conn, post.Id, owner.Id, "old-worker", "stale verdict"))
	require.NoError(t, conn.First(&owner, owner.Id).Error)
	require.False(t, owner.IsBan)
}

func TestModeratorCannotReleaseSuspendedAuthorAndCanRetryFlaggedPost(t *testing.T) {
	conn, fix, owner := replacementDB(t)
	require.NoError(t, conn.AutoMigrate(newacctmod.Models()...))
	post := fix.CreateStatus(owner, func(s *models.Status) { s.LocalOnly = true })
	job := newacctmod.ReviewJob{StatusID: post.Id, AccountID: owner.Id, State: "done", Verdict: "flag", Attempts: 5, LeaseToken: "old-worker"}
	require.NoError(t, conn.Create(&job).Error)
	require.NoError(t, conn.Model(owner).UpdateColumn("is_ban", true).Error)
	require.ErrorIs(t, newacctmod.ReviewManually(context.Background(), conn, job.ID, owner.Id, "approve"), newacctmod.ErrReviewClosed)
	require.NoError(t, conn.Model(owner).UpdateColumn("is_ban", false).Error)
	require.NoError(t, newacctmod.ReviewManually(context.Background(), conn, job.ID, owner.Id, "retry"))
	require.NoError(t, conn.First(&job, job.ID).Error)
	require.Equal(t, "pending", job.State)
	require.Empty(t, job.Verdict)
	require.Empty(t, job.LeaseToken)
	require.Zero(t, job.Attempts)
}

func TestAutomaticReviewDoesNotBanRepeatedOrdinaryGreetings(t *testing.T) {
	conn, fix, owner := replacementDB(t)
	require.NoError(t, conn.AutoMigrate(&models.Report{}))
	require.NoError(t, conn.AutoMigrate(newacctmod.Models()...))
	for i := 0; i < 2; i++ {
		fix.CreateStatus(owner, func(s *models.Status) { s.Content.Content = "good morning everyone" })
	}
	post := fix.CreateStatus(owner, func(s *models.Status) { s.LocalOnly = true; s.Content.Content = "good morning everyone" })
	job := newacctmod.ReviewJob{StatusID: post.Id, AccountID: owner.Id, State: "pending"}
	require.NoError(t, conn.Create(&job).Error)
	require.NoError(t, newacctmod.Process(context.Background(), conn, post.Id))
	require.NoError(t, conn.First(owner, owner.Id).Error)
	require.False(t, owner.IsBan)
	require.NoError(t, conn.First(&post, post.Id).Error)
	require.NoError(t, conn.First(&job, job.ID).Error)
	require.Equal(t, "flag", job.Verdict)
	var reports int64
	require.NoError(t, conn.Model(&models.Report{}).Where("target_account_id = ?", owner.Id).Count(&reports).Error)
	require.EqualValues(t, 1, reports)
}
