package datatransfer

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// SourceIssueCode replaces exporter codes that are not on the allowlist.
const SourceIssueCode = "source_issue"

// maxSourceIssueSamples bounds the per-record samples one aggregated source
// issue carries; Count always holds the full total.
const maxSourceIssueSamples = 50

// IssueSample is one record of an aggregated source issue: its source id
// and the allowlisted, sanitized details the exporter attached.
type IssueSample struct {
	SourceID string         `json:"source_id,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
}

// sourceIssueSpec is what the report may say about one exporter issue code:
// a static message (the exporter's own message never crosses into the
// report) and the detail fields that are known to be non-sensitive.
type sourceIssueSpec struct {
	message string
	details []string
}

// sourceIssueSpecs is the allowlist. A code missing here is reported as
// source_issue with no ids, details or text from the export.
var sourceIssueSpecs = func() map[string]sourceIssueSpec {
	m := map[string]sourceIssueSpec{
		"misskey_meta_asset":             {"Ownerless local drive file has no Community equivalent and was not migrated; details.usage names what referenced it.", []string{"usage", "file_name", "size", "content_type"}},
		"object_storage_reference":       {"Object-storage files keep their original URL; the bucket must stay available to the target.", []string{"count"}},
		"unattached_drive_files_skipped": {"Local drive files referenced by nothing were not exported (--skip-unattached-drive-files).", []string{"count"}},
		"remote_reference_trimmed":       {"A reference to a record outside the export was dropped; the raw source row keeps it.", nil},
		"source_records_preserved":       {"Complete source rows of this table are preserved privately; preservation does not imply live support.", nil},
		"source_id_method":               {"Source ID timestamp method used by the export.", nil},
	}
	for _, code := range []string{"invalid_source_time", "raw_records_preserved", "source_auth_material", "missing_actor_uri", "missing_local_profile", "nonlocal_private_key", "missing_local_key", "media_bytes_unavailable", "media_variant_unavailable", "external_storage_required", "remote_media_reference", "unknown_visibility", "channel_note_archive", "quote_note_archive", "chat_archive_only", "chat_policy_review", "invalid_poll_counts", "emoji_bytes_unavailable", "source_style_absent", "missing_or_unreadable_media", "password_pepper_unsupported", "media_has_no_original", "emoji_has_no_original", "invalid_remote_media_reference", "local_actor_without_login", "two_factor_requires_adapter", "missing_password_verifier", "unsupported_active_key", "missing_local_signing_key", "unsupported_limited_visibility", "missing_account_emoji", "missing_note_emoji"} {
		m[code] = sourceIssueSpec{message: "Source backup: " + strings.ReplaceAll(code, "_", " ") + ". Review the private source export and its migration requirements."}
	}
	return m
}()

var (
	safeSourceIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._-]{0,63}$`)
	safeSourceKindPattern = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)
	// usage tokens from export_misskey.py: meta.<column>[,meta.<column>...],
	// <table>.<column> or unreferenced.
	safeUsagePattern = regexp.MustCompile(`^(unreferenced|[a-z_]{1,32}\.[A-Za-z0-9_]{1,40}(,[a-z_]{1,32}\.[A-Za-z0-9_]{1,40}){0,7})$`)
	safeContentType  = regexp.MustCompile(`^[a-z0-9.+-]{1,40}/[A-Za-z0-9.+-]{1,80}$`)
)

// safeSourceIssue maps an exporter code to the code and static message the
// report may carry.
func safeSourceIssue(code string) (string, string) {
	if spec, ok := sourceIssueSpecs[code]; ok {
		return code, spec.message
	}
	return SourceIssueCode, "source backup reports an issue; inspect its private export records"
}

// sourceIssueDetails keeps only the allowlisted detail fields of code, each
// checked against the shape that field may have.
func sourceIssueDetails(code string, in map[string]any) map[string]any {
	spec := sourceIssueSpecs[code]
	out := map[string]any{}
	for _, key := range spec.details {
		v, ok := in[key]
		if !ok {
			continue
		}
		switch key {
		case "size", "count":
			if n, ok := v.(float64); ok && n >= 0 && n <= math.MaxInt64 && n == math.Trunc(n) {
				out[key] = int64(n)
			}
		case "usage":
			if s, ok := v.(string); ok && safeUsagePattern.MatchString(s) {
				out[key] = s
			}
		case "content_type":
			if s, ok := v.(string); ok && safeContentType.MatchString(s) {
				out[key] = s
			}
		case "file_name":
			if s, ok := v.(string); ok {
				if s = safeFileName(s); s != "" {
					out[key] = s
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// safeFileName strips control and format characters and bounds the length.
func safeFileName(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == unicode.ReplacementChar {
			continue
		}
		if n++; n > 128 {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// sourceIssues folds the exporter's issues into one report issue per
// (severity, code, source kind): a count, up to maxWidthSamples ids and up to
// maxSourceIssueSamples samples with details. A site import is run by the
// operator from their own source database, so allowlisted codes keep their
// source ids and details; a personal upload's ids and details never cross.
func sourceIssues(in []Issue, site bool) []Issue {
	type group struct {
		is  Issue
		ids map[string]bool
	}
	var order []string
	groups := map[string]*group{}
	for _, v := range in {
		severity := "warning"
		switch v.Severity {
		case "error", "info":
			severity = v.Severity
		}
		code, message := safeSourceIssue(v.Code)
		known := code != SourceIssueCode
		kind, id, details := "", "", map[string]any(nil)
		if known && site {
			if safeSourceKindPattern.MatchString(v.Kind) {
				kind = v.Kind
			}
			if safeSourceIDPattern.MatchString(v.SourceID) {
				id = v.SourceID
			}
			details = sourceIssueDetails(code, v.Details)
		}
		key := severity + "\x00" + code + "\x00" + kind
		g := groups[key]
		if g == nil {
			g = &group{is: Issue{Severity: severity, Code: code, Kind: "source_export", SourceKind: kind, Message: message}, ids: map[string]bool{}}
			groups[key] = g
			order = append(order, key)
		}
		if id != "" {
			if g.ids[id] && details == nil {
				continue
			}
			g.ids[id] = true
		}
		g.is.Count++
		if id != "" && len(g.is.SampleIDs) < maxWidthSamples && !contains(g.is.SampleIDs, id) {
			g.is.SampleIDs = append(g.is.SampleIDs, id)
		}
		if (id != "" || details != nil) && len(g.is.Samples) < maxSourceIssueSamples {
			g.is.Samples = append(g.is.Samples, IssueSample{SourceID: id, Details: details})
		}
	}
	out := make([]Issue, 0, len(order))
	for _, key := range order {
		is := groups[key].is
		sort.Strings(is.SampleIDs)
		is.SourceID = firstOr(is.SampleIDs)
		if is.Count > 1 || len(is.Samples) < is.Count {
			is.Message += fmt.Sprintf(" (%d record(s)", is.Count)
			if len(is.Samples) < is.Count && len(is.Samples) > 0 {
				is.Message += fmt.Sprintf("; first %d listed in samples", len(is.Samples))
			}
			is.Message += ")"
		}
		out = append(out, is)
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
