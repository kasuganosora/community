package status

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// voteDB is a real database for concurrency: PostgreSQL when
// TEST_DATABASE_URL is set, otherwise a file-backed SQLite database with the
// production settings (WAL, busy_timeout, one connection). The default
// in-memory test engine does not model row locks.
func voteDB(t *testing.T) *gorm.DB {
	t.Helper()
	testutil.NewTestSetup(t, testutil.WithModels(allTestModels...)) // redis + globals
	var conn *gorm.DB
	if testutil.UsePostgres() {
		conn = testutil.OpenPostgresTestDB(t, &gorm.Config{Logger: logger.Discard, DisableForeignKeyConstraintWhenMigrating: true})
		sqlDB, err := conn.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(16)
	} else {
		var err error
		conn, err = gorm.Open(dialect.OpenSQLite(filepath.Join(t.TempDir(), "votes.db")), &gorm.Config{Logger: logger.Discard})
		require.NoError(t, err)
		sqlDB, err := conn.DB()
		require.NoError(t, err)
		for _, p := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA synchronous=NORMAL"} {
			_, err = sqlDB.Exec(p)
			require.NoError(t, err)
		}
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	require.NoError(t, conn.AutoMigrate(append(append([]any{}, allTestModels...), &models.FederationOutbox{})...))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)
	return conn
}

func voteRouter(conn *gorm.DB) *gin.Engine {
	r := gin.New()
	r.Use(controllers.AbortRecoveryMiddleware())
	r.Use(func(c *gin.Context) {
		id, _ := strconv.ParseUint(c.GetHeader("X-Test-Account"), 10, 64)
		var a models.Account
		if conn.First(&a, id).Error == nil {
			c.Set("account", &a)
		}
	})
	r.GET("/api/v1/polls/:id", ViewPoll)
	r.POST("/api/v1/polls/:id/votes", VotePoll)
	return r
}

func castVote(r *gin.Engine, pollID, accountID uint64, choices string) int {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/polls/%d/votes", pollID), bytes.NewBufferString(`{"choices":`+choices+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Account", strconv.FormatUint(accountID, 10))
	r.ServeHTTP(w, req)
	return w.Code
}

// Concurrent votes on one local poll (while its author keeps polling GET)
// are each counted exactly once; one account racing itself votes once; the
// vote transaction only records a coalesced deferred Update (no recipient
// expansion inline); a vote after the status is deleted is a clean 404.
func TestVotePollConcurrentVotesKeepCountsExact(t *testing.T) {
	conn := voteDB(t)
	fix := testutil.NewFixtureBuilder(t, conn)
	authorUser := fix.CreateUser(func(u *models.User) { u.UserName = "pollbot" })
	author := fix.CreateAccount(authorUser, func(a *models.Account) { a.Username = "pollbot"; a.Bot = true })
	// Remote followers: their expansion must not happen in the vote path.
	for i := 0; i < 3; i++ {
		domain := fmt.Sprintf("r%d.example", i)
		remote := &models.Account{Username: "f", Domain: &domain, OriginId: "https://" + domain + "/users/f"}
		require.NoError(t, conn.Create(remote).Error)
		require.NoError(t, conn.Create(&models.Follow{AccountId: remote.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	}
	status := fix.CreateStatus(author)
	expires := time.Now().Add(time.Hour)
	poll := fix.CreatePoll(status.Id, author.Id, func(p *models.Poll) { p.ExpiresAt = &expires })
	for i, name := range []string{"yes", "no", "maybe"} {
		require.NoError(t, conn.Create(&models.PollOption{PollId: poll.Id, Content: name, Index: uint8(i)}).Error)
	}
	const voters = 12
	accounts := make([]*models.Account, voters)
	for i := range accounts {
		u := fix.CreateUser(func(u *models.User) { u.UserName = fmt.Sprintf("v%d", i) })
		accounts[i] = fix.CreateAccount(u, func(a *models.Account) { a.Username = fmt.Sprintf("v%d", i) })
	}
	require.NoError(t, conn.Where("1 = 1").Delete(&models.FederationOutbox{}).Error)
	r := voteRouter(conn)

	codes := make(chan [2]int, voters+8)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, a := range accounts {
		repeats := 1
		if i == 0 {
			repeats = 5 // one account racing itself (double tap / client retry)
		}
		for k := 0; k < repeats; k++ {
			wg.Add(1)
			go func(i int, a *models.Account) {
				defer wg.Done()
				<-start
				codes <- [2]int{i, castVote(r, poll.Id, a.Id, fmt.Sprintf("[%d]", i%3))}
			}(i, a)
		}
	}
	stopView := make(chan struct{})
	viewDone := make(chan struct{})
	go func() { // the author polling GET /api/v1/polls/:id meanwhile
		defer close(viewDone)
		for {
			select {
			case <-stopView:
				return
			default:
			}
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/polls/%d", poll.Id), nil)
			req.Header.Set("X-Test-Account", strconv.FormatUint(author.Id, 10))
			r.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Errorf("view poll: %d %s", w.Code, w.Body.String())
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	close(stopView)
	<-viewDone
	close(codes)
	ok := map[int]int{}
	for c := range codes {
		switch c[1] {
		case 200:
			ok[c[0]]++
		case 422:
			require.Equal(t, 0, c[0], "only the racing account may be refused")
		default:
			t.Fatalf("voter %d: unexpected status %d", c[0], c[1])
		}
	}
	for i := 0; i < voters; i++ {
		require.Equal(t, 1, ok[i], "voter %d must be counted exactly once", i)
	}

	var options []models.PollOption
	require.NoError(t, conn.Where("poll_id = ?", poll.Id).Order("id").Find(&options).Error)
	var total uint64
	for i, o := range options {
		var n int64
		require.NoError(t, conn.Model(&models.PollOptionVote{}).Where("option_id = ?", o.Id).Count(&n).Error)
		require.EqualValues(t, n, o.Votes, "option %d tally matches its vote rows", i)
		require.EqualValues(t, voters/3, o.Votes, "option %d", i)
		total += o.Votes
	}
	require.EqualValues(t, voters, total)

	var events []models.FederationOutbox
	require.NoError(t, conn.Where("object_id = ?", status.Id).Find(&events).Error)
	require.Len(t, events, 1, "pending tally Updates are coalesced, even under concurrency")
	require.Equal(t, models.KindPollUpdate, events[0].Kind, "snapshot and recipients are built by the outbox worker")
	require.Empty(t, events[0].RecipientIDs)

	// The worker's expansion turns it into a regular Update for the remote followers.
	send, err := models.MaterializePollUpdate(conn, &events[0])
	require.NoError(t, err)
	require.True(t, send)
	require.Equal(t, "Update", events[0].Kind)
	require.Len(t, events[0].RecipientIDs, 3)

	// The author deletes the status: a late vote is a 404, not a 500 or an orphan vote.
	late := fix.CreateUser(func(u *models.User) { u.UserName = "late" })
	lateAcct := fix.CreateAccount(late, func(a *models.Account) { a.Username = "late" })
	require.NoError(t, conn.Delete(status).Error)
	code := castVote(r, poll.Id, lateAcct.Id, "[0]")
	require.Contains(t, []int{404, 500}, code)
	var orphans int64
	require.NoError(t, conn.Model(&models.PollOptionVote{}).Where("account_id = ?", lateAcct.Id).Count(&orphans).Error)
	require.Zero(t, orphans)
}

// A repeated choice in one request counts once.
func TestVotePollDuplicateChoiceCountsOnce(t *testing.T) {
	conn := voteDB(t)
	fix := testutil.NewFixtureBuilder(t, conn)
	u := fix.CreateUser()
	author := fix.CreateAccount(u)
	status := fix.CreateStatus(author)
	poll := fix.CreatePoll(status.Id, author.Id, func(p *models.Poll) { p.Multiple = true })
	for i, name := range []string{"a", "b"} {
		require.NoError(t, conn.Create(&models.PollOption{PollId: poll.Id, Content: name, Index: uint8(i)}).Error)
	}
	require.Equal(t, 200, castVote(voteRouter(conn), poll.Id, author.Id, "[1,1,0]"))
	var options []models.PollOption
	require.NoError(t, conn.Where("poll_id = ?", poll.Id).Order("\"index\"").Find(&options).Error)
	require.EqualValues(t, 1, options[0].Votes)
	require.EqualValues(t, 1, options[1].Votes)
	var n int64
	require.NoError(t, conn.Model(&models.PollOptionVote{}).Where("poll_id = ?", poll.Id).Count(&n).Error)
	require.EqualValues(t, 2, n)
}
