package datatransfer

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// Misskey renotes with their own content are quotes. They are imported as
// normal statuses and linked to their target through statuses.quote_id, the
// column c.hime.at's own quotes use. Linking happens after the post exists,
// so ordering never matters: within a bundle right after the posts phase,
// and in a streamed run once more after the posts stage for targets that
// came in a later chunk.
//
// Like a quote composed on the target (status create API), only a public or
// unlisted target is linked: a followers-only or direct target is never
// exposed through a quote. Unlinked quotes stay normal posts that keep the
// target URI in properties.import_quote_uri.
const (
	// QuoteUnlinkedCode is the non-blocking issue for quotes imported
	// without quote_id, per reason (Kind).
	QuoteUnlinkedCode = "quote_unlinked"

	quoteReasonMissing     = "target_missing"
	quoteReasonArchived    = "target_archived"
	quoteReasonNotQuotable = "target_not_quotable"

	quotePendingProperty = "import_quote_pending"
	quoteSourceProperty  = "import_quote_source_id"
	quoteURIProperty     = "import_quote_uri"
	quoteUnlinkedProp    = "import_quote_unlinked"
	quoteOwnSourceProp   = "import_quote_pending_source"
)

func quoteUnlinkedIssue(reason string, count int, samples, violators []string) Issue {
	what := map[string]string{
		quoteReasonMissing:     "the quoted post is not part of the import",
		quoteReasonArchived:    "the quoted post is kept in the private archive",
		quoteReasonNotQuotable: "the quoted post is followers-only or direct and must not be exposed by a quote",
	}[reason]
	return Issue{Severity: "warning", Code: QuoteUnlinkedCode, Kind: "post:" + reason, SourceID: firstOr(samples), Count: count, SampleIDs: samples, violators: violators,
		Message: fmt.Sprintf("%d quote(s) imported as normal posts without a quote link (%s); the target URI is kept in properties.import_quote_uri; sample source ids: %s", count, what, strings.Join(samples, ","))}
}

// quotable reports whether a target of the given visibility may be linked.
func quotable(v uint8) bool {
	return v == models.StatusVisibilityPublic || v == models.StatusVisibilityUnlisted
}

// postReceipt returns the target id of an imported (or archived) post.
func postReceipt(tx *gorm.DB, namespace, id string) (live uint64, archived bool, err error) {
	var rows []ImportReceipt
	if err = tx.Select("kind", "target_id").Where("namespace = ? AND kind IN ? AND source_hash = ?", namespace, []string{"post", ArchivedPostKind}, digestBytes([]byte(id))).Find(&rows).Error; err != nil {
		return 0, false, err
	}
	for _, r := range rows {
		if r.Kind == ArchivedPostKind {
			archived = true
		} else if r.TargetID > 0 {
			live = r.TargetID
		}
	}
	return live, archived, nil
}

// linkQuote settles one imported quote. final decides unresolved targets as
// missing; otherwise they stay pending for a later pass. It returns the
// unlinked reason ("" when linked or still pending).
func linkQuote(tx *gorm.DB, namespace string, statusID uint64, quoteSource string, final bool) (string, error) {
	target, archived, err := postReceipt(tx, namespace, quoteSource)
	if err != nil {
		return "", err
	}
	reason := ""
	var targetID uint64
	switch {
	case target != 0:
		var quoted models.Status
		if err := tx.Select("id", "visibility", "reblog_id").First(&quoted, target).Error; err != nil {
			return "", err
		}
		// A quote of a boost quotes the boosted post (as the create API does).
		if quoted.ReblogId != nil && *quoted.ReblogId > 0 {
			if err := tx.Select("id", "visibility", "reblog_id").First(&quoted, *quoted.ReblogId).Error; err != nil {
				return "", err
			}
		}
		if quotable(quoted.Visibility) {
			targetID = quoted.Id
		} else {
			reason = quoteReasonNotQuotable
		}
	case archived:
		reason = quoteReasonArchived
	case final:
		reason = quoteReasonMissing
	default:
		return "", nil
	}
	var status models.Status
	if err := tx.Select("id", "properties").First(&status, statusID).Error; err != nil {
		return "", err
	}
	if status.Properties == nil {
		status.Properties = map[string]any{}
	}
	delete(status.Properties, quotePendingProperty)
	updates := map[string]any{}
	if targetID != 0 {
		updates["quote_id"] = targetID
	} else {
		status.Properties[quoteUnlinkedProp] = reason
	}
	delete(status.Properties, quoteOwnSourceProp)
	raw, err := json.Marshal(status.Properties)
	if err != nil {
		return "", err
	}
	updates["properties"] = string(raw)
	return reason, tx.Model(&models.Status{}).Where("id = ?", statusID).UpdateColumns(updates).Error
}

// linkBundleQuotes links the quotes created by this importer run.
func (i *importer) linkBundleQuotes() error {
	for _, q := range i.quotes {
		reason, err := linkQuote(i.tx, i.namespace, q.status, q.quote, !i.options.DeferQuoteLinks)
		if err != nil {
			return err
		}
		if reason != "" {
			i.report.Issues = append(i.report.Issues, quoteUnlinkedIssue(reason, 1, []string{q.source}, []string{q.source}))
		}
	}
	i.quotes = nil
	return nil
}

type pendingQuote struct {
	status        uint64
	source, quote string
}

// finishQuotes is the deferred link pass of a streamed import: every quote
// still pending (its target came in a later chunk, or never) is settled.
func (r *streamRun) finishQuotes() error {
	conn := r.conn.WithContext(r.ctx)
	var after uint64
	for {
		var rows []models.Status
		if err := conn.Select("id", "properties").Where("id > ? AND "+dialect.JSONText(conn, "properties", quotePendingProperty)+" = ?", after, r.namespace).
			Order("id").Limit(500).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		err := conn.Transaction(func(tx *gorm.DB) error {
			for _, s := range rows {
				quote, _ := s.Properties[quoteSourceProperty].(string)
				source, _ := s.Properties[quoteOwnSourceProp].(string)
				reason, err := linkQuote(tx, r.namespace, s.Id, quote, true)
				if err != nil {
					return err
				}
				if reason != "" {
					r.widths.add(quoteUnlinkedIssue(reason, 1, []string{source}, []string{source}))
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		after = rows[len(rows)-1].Id
	}
}

// previewQuotes predicts, after the posts stage of a streamed preview, which
// quotes the import will leave unlinked, from the effective visibility the
// posts preview recorded for every post (archived and unpublishable posts
// carry sentinels).
func (r *streamRun) previewQuotes() error {
	rows, err := r.sp.db.Query(`SELECT p.id, json_extract(p.raw, '$.quote_id'), json_extract(t.raw, '$.reblog_id')
		FROM rec p LEFT JOIN rec t ON t.section = 'posts' AND t.id = json_extract(p.raw, '$.quote_id')
		WHERE p.section = 'posts' AND coalesce(json_extract(p.raw, '$.quote_id'), '') <> '' ORDER BY p.seq`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, quote string
		var reblog *string
		if err := rows.Scan(&id, &quote, &reblog); err != nil {
			return err
		}
		if reblog != nil && *reblog != "" {
			quote = *reblog
		}
		if reason := r.quoteOutcome(quote); reason != "" {
			r.widths.add(quoteUnlinkedIssue(reason, 1, []string{id}, []string{id}))
		}
	}
	return rows.Err()
}

func (r *streamRun) quoteOutcome(quote string) string {
	v, ok := r.o.ParentVisibility[quote]
	if !ok {
		if r.o.Seed[ArchivedPostKind][quote] != 0 {
			return quoteReasonArchived
		}
		if t := r.o.Seed["post"][quote]; t > 1 {
			var s models.Status
			if r.conn.Select("visibility").First(&s, t).Error == nil && !quotable(s.Visibility) {
				return quoteReasonNotQuotable
			}
			return ""
		}
		return quoteReasonMissing
	}
	switch {
	case v == knownArchived || v == knownDead:
		return quoteReasonArchived
	case !quotable(v):
		return quoteReasonNotQuotable
	}
	return ""
}
