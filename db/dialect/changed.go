package dialect

import (
	"sort"
	"strings"

	"gorm.io/gorm"
)

// WhereChanged restricts q to rows where at least one of the given columns
// differs from its new value (NULL-safe). Updates that would rewrite a row
// with the values it already has then match nothing and cost no write (a
// PostgreSQL UPDATE writes a new row version even when nothing changes).
func WhereChanged(q *gorm.DB, values map[string]any) *gorm.DB {
	if len(values) == 0 {
		return q
	}
	cols := make([]string, 0, len(values))
	for c := range values {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	parts := make([]string, 0, len(cols))
	args := make([]any, 0, len(cols))
	for _, c := range cols {
		col := Quote(q, c)
		if IsMySQL(q) {
			parts = append(parts, "NOT ("+col+" <=> ?)")
		} else {
			parts = append(parts, col+" IS DISTINCT FROM ?")
		}
		args = append(args, values[c])
	}
	return q.Where("("+strings.Join(parts, " OR ")+")", args...)
}

// UpdateChanged applies values (plus extra columns such as updated_at, which
// are not compared) only when some value differs from what is stored.
func UpdateChanged(q *gorm.DB, values, extra map[string]any) *gorm.DB {
	all := make(map[string]any, len(values)+len(extra))
	for k, v := range values {
		all[k] = v
	}
	for k, v := range extra {
		all[k] = v
	}
	return WhereChanged(q, values).Updates(all)
}

// AsyncCommit relaxes durability for the current transaction on PostgreSQL
// (SET LOCAL synchronous_commit = off): the commit returns before its WAL
// is flushed, so a crash can lose the last few hundred milliseconds of
// such transactions but never corrupts data. Use it only for federation
// data that the remote server will resend or that is refetched anyway
// (inbox posts, remote profiles, delivery bookkeeping), never for local
// user data. It is a no-op outside PostgreSQL.
func AsyncCommit(tx *gorm.DB) {
	if IsPostgres(tx) {
		tx.Exec("SET LOCAL synchronous_commit = off")
	}
}
