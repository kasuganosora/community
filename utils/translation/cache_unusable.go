package translation

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/models"
)

// UnusableCacheEntry describes a cached translation that is not a
// translation (see unusableText).
type UnusableCacheEntry struct {
	ID       uint64
	Target   string
	Provider string
	Reason   string
	StatusID uint64 // 0 when no status references the content (e.g. group chat)
}

// FindUnusableCache scans the translation cache for entries whose output is
// not a translation of their source: the source echoed back (after
// normalisation) or text not in the target's script. The source text is
// recovered from a status referencing the entry's content hash; entries
// without one (group chat) are judged by the output script alone.
// When purge is true the matching entries are deleted.
func FindUnusableCache(purge bool) (scanned int, found []UnusableCacheEntry, err error) {
	conn := db.GetDB()
	if conn == nil {
		return 0, nil, nil
	}
	var lastID uint64
	for {
		var rows []llmsvc.TranslationCache
		if err = conn.Where("id > ?", lastID).Order("id").Limit(500).Find(&rows).Error; err != nil {
			return scanned, found, err
		}
		if len(rows) == 0 {
			break
		}
		for i := range rows {
			row := &rows[i]
			lastID = row.ID
			scanned++
			out, ok := decodeCached(row.Result)
			if !ok {
				continue
			}
			entry := UnusableCacheEntry{ID: row.ID, Target: row.TargetLang, Provider: row.Provider}
			var ref llmsvc.TranslationCacheRef
			if row.ContentHash != "" && conn.Where("content_hash = ?", row.ContentHash).Limit(1).Find(&ref).Error == nil && ref.ID != 0 {
				var s models.Status
				if conn.Preload("Content").Where("id = ?", ref.StatusID).Limit(1).Find(&s).Error == nil && s.Id != 0 {
					p, _, _ := payloadOf(&s)
					entry.StatusID = s.Id
					entry.Reason = traditionalUnusable(p, out, "", row.TargetLang)
				}
			}
			if entry.StatusID == 0 {
				entry.Reason = outputWrongScript(out, row.TargetLang)
			}
			if entry.Reason == "" {
				continue
			}
			found = append(found, entry)
			if purge {
				if err = conn.Where("id = ?", row.ID).Delete(&llmsvc.TranslationCache{}).Error; err != nil {
					return scanned, found, err
				}
			}
		}
	}
	return scanned, found, nil
}

func decodeCached(result string) (*llmsvc.TranslationOutput, bool) {
	raw, err := base64.StdEncoding.DecodeString(result)
	if err != nil {
		return nil, false
	}
	var c cachedResult
	if json.Unmarshal(raw, &c) != nil {
		return nil, false
	}
	return &c.TranslationOutput, true
}

// outputWrongScript judges an output without its source: for CJK targets,
// output with enough letters but almost none in the target script.
func outputWrongScript(out *llmsvc.TranslationOutput, target string) string {
	switch primary(target) {
	case "zh", "ja", "ko":
	default:
		return ""
	}
	text := htmlsafe.StripTags(out.Content)
	if strings.TrimSpace(text) == "" {
		text = out.SpoilerText
	}
	share, letters := scriptShare(text, targetScript(target))
	if letters >= minScriptLetters && share < minScriptShare {
		return "wrong_script"
	}
	return ""
}
