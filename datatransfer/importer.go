package datatransfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type importer struct {
	tx         *gorm.DB
	bundle     *Bundle
	options    Options
	namespace  string
	ids        map[string]map[string]uint64
	report     *Report
	mediaPaths map[string]string
}

type importFailure struct {
	cause        error
	phase        string
	databaseCode uint16
}

func (e *importFailure) Error() string {
	return fmt.Sprintf("transfer transaction failed in %s (database code %d); no records from this attempt were committed", e.phase, e.databaseCode)
}
func (e *importFailure) Unwrap() error { return e.cause }

func Import(ctx context.Context, conn *gorm.DB, b *Bundle, o Options, onProgress func(Progress)) (*Report, error) {
	conn = conn.Session(&gorm.Session{Logger: logger.Discard})
	report, err := Preview(ctx, conn, b, o)
	if err != nil {
		return report, err
	}
	if !report.CanExecute {
		return report, errors.New("transfer preflight failed; inspect report issues")
	}
	mediaPaths, cleanup, err := stageImportMedia(ctx, b, o)
	if err != nil {
		return report, err
	}
	committed := false
	defer func() {
		if !committed {
			cleanup()
		}
	}()
	if onProgress != nil {
		onProgress(Progress{Phase: "importing", Total: 7})
	}
	phase := "begin"
	for attempt := 0; attempt < 3; attempt++ {
		report.Created = map[string]int{}
		report.Skipped = map[string]int{}
		report.Archived = map[string]int{}
		runner := &importer{bundle: b, options: o, namespace: transferNamespace(b, o), ids: map[string]map[string]uint64{}, report: report, mediaPaths: mediaPaths}
		for kind, m := range o.Seed {
			for id, target := range m {
				runner.remember(kind, id, target)
			}
		}
		err = conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if o.LeaseCheck != nil {
				if err := o.LeaseCheck(tx); err != nil {
					return err
				}
			}
			runner.tx = tx.Session(&gorm.Session{SkipHooks: true}).Omit(clause.Associations).Session(&gorm.Session{})
			phases := []struct {
				name string
				run  func() error
			}{
				{"accounts", runner.importAccounts}, {"media", runner.importMedia}, {"emojis", runner.importEmojis}, {"posts", runner.importPosts},
				{"relations", runner.importRelations}, {"extras", runner.importExtras}, {"preserved", runner.importPreserved},
			}
			for _, p := range phases {
				phase = p.name
				if err := ctx.Err(); err != nil {
					return err
				}

				if err := p.run(); err != nil {
					return fmt.Errorf("%s import failed: %w", p.name, err)
				}
			}
			if err := models.QueueFeedInvalidation(tx, models.FeedEventMetadata{Reason: "import_boundary"}); err != nil {
				return err
			}
			if o.LeaseCheck != nil {
				return o.LeaseCheck(tx)
			}
			return ctx.Err()
		})
		if err == nil {
			committed = true
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			cacheErr := runner.invalidateCaches(cleanupCtx, conn)
			cancel()
			if cacheErr != nil {
				report.Issues = append(report.Issues, Issue{Severity: "warning", Code: "cache_refresh_pending", Message: "data committed successfully; timeline cache refresh is pending; retry this import or restart the instance before cutover"})
			}
			if onProgress != nil {
				onProgress(Progress{Phase: "complete", Completed: 7, Total: 7})
			}
			return report, nil
		}
		var sqlErr *mysql.MySQLError
		if !errors.As(err, &sqlErr) || (sqlErr.Number != 1213 && sqlErr.Number != 1205) {
			break
		}
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 25 * time.Millisecond):
		}
	}
	report.Created = map[string]int{}
	report.Skipped = map[string]int{}
	report.Archived = map[string]int{}
	// Database errors may include a credential-bearing INSERT. Return no SQL or
	// bound values through the public worker report.
	var databaseCode uint16
	var databaseError *mysql.MySQLError
	if errors.As(err, &databaseError) {
		databaseCode = databaseError.Number
	}
	failure := &importFailure{cause: err, phase: phase, databaseCode: databaseCode}
	report.Issues = append(report.Issues, Issue{Severity: "error", Code: "import_failed", Message: failure.Error()})
	return report, failure
}
func (i *importer) importPreserved() error {
	for _, r := range i.bundle.Records {
		var value any = r
		if r.Kind == "post" {
			var post Post
			if json.Unmarshal(r.Data, &post) == nil && post.ID != "" && post.AuthorID != "" {
				value = post
			}
		}
		stored := value
		if redacted, changed := RedactSecretFields(r.Data); changed {
			copyRecord := r
			copyRecord.Data = redacted
			stored = copyRecord
		}
		if err := i.preserveStored(r.Kind, r.ID, value, stored); err != nil {
			return err
		}
	}
	return nil
}
func timePointer(v time.Time) *time.Time {
	if v.IsZero() {
		return nil
	}
	return &v
}
func (i *importer) personal() bool { return i.options.Scope == "personal" }
func (i *importer) sourceAccount(id string) Account {
	for _, a := range i.bundle.Accounts {
		if a.ID == id {
			return a
		}
	}
	return Account{}
}
func (i *importer) archivePost(p Post) bool {
	if p.ArchiveOnly || (i.personal() && p.AuthorID != i.bundle.OwnerID) {
		return true
	}
	if i.personal() {
		for _, ref := range p.RecipientIDs {
			if i.ids["account"][ref] == 0 {
				return true
			}
		}
		for _, ref := range p.MediaIDs {
			if i.ids["media"][ref] == 0 {
				return true
			}
		}
		for _, ref := range p.EmojiIDs {
			if i.ids["emoji"][ref] == 0 {
				return true
			}
		}
	}
	return false
}
