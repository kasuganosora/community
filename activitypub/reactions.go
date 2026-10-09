package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

func reactionContent(activity map[string]any) string {
	content, _ := activity["_misskey_reaction"].(string)
	if content == "" {
		content, _ = activity["content"].(string)
	}
	return content
}
func reactionStatus(conn *gorm.DB, uri string) (*models.Status, error) {
	var s models.Status
	err := conn.Preload("Account").Preload("ToAccounts").Where("origin_uri = ? OR local_object_uri = ?", uri, uri).First(&s).Error
	if err == nil {
		return &s, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	u, e := url.Parse(uri)
	if e != nil {
		return nil, gorm.ErrRecordNotFound
	}
	id, e := strconv.ParseUint(path.Base(u.Path), 10, 64)
	if e != nil {
		return nil, gorm.ErrRecordNotFound
	}
	if err = conn.Preload("Account").Preload("ToAccounts").First(&s, id).Error; err != nil {
		return nil, err
	}
	if !s.IsLocal() || s.Account == nil || statusObjectURI(&s, s.Account, systemsettings.GetBaseURL()) != uri {
		return nil, gorm.ErrRecordNotFound
	}
	return &s, nil
}

// Signature verification provides actorKey; embedded Undo actor must match it
// and the exact stored original activity ID must belong to that actor.
func handleEmojiReaction(ctx context.Context, c *gin.Context, activity map[string]any, undo bool) {
	actorURI, _ := ctx.Value(actorKey).(string)
	original := activity
	if undo {
		var ok bool
		original, ok = activity["object"].(map[string]any)
		if !ok {
			c.JSON(400, gin.H{"error": "invalid undo"})
			return
		}
		embedded, _ := original["actor"].(string)
		if embedded != actorURI {
			c.JSON(403, gin.H{"error": "actor mismatch"})
			return
		}
	}
	content := reactionContent(original)
	object, _ := original["object"].(string)
	activityURI, _ := original["id"].(string)
	a, _ := url.Parse(actorURI)
	id, _ := url.Parse(activityURI)
	if a == nil || id == nil || a.Host == "" || id.Host != a.Host || id.Scheme != a.Scheme {
		c.JSON(400, gin.H{"error": "invalid reaction activity ID"})
		return
	}
	actor, err := getOrCreateRemoteAccountContext(ctx, actorURI)
	if err != nil {
		respondRemoteAccountError(c, actorURI, err)
		return
	}
	s, err := reactionStatus(activityDB(ctx), object)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(202, gin.H{"status": "ignored"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "target lookup failed"})
		return
	}
	allowed, err := s.CanViewEx(activityDB(ctx), actor)
	if err != nil {
		c.JSON(500, gin.H{"error": "permission lookup failed"})
		return
	}
	if !allowed {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}
	var emoji *models.Emoji
	if strings.HasPrefix(content, ":") {
		// Misskey serializes local custom reactions with :shortcode:. Never trust
		// an actor-supplied @domain to overwrite another instance's namespace.
		match := emojiName.FindStringSubmatch(content)
		if match == nil || actor.Domain == nil {
			c.JSON(422, gin.H{"error": "invalid custom reaction"})
			return
		}
		if !undo {
			emojis, e := importEmojiTags(activityDB(ctx), *actor.Domain, content, original["tag"])
			if e != nil {
				c.JSON(500, gin.H{"error": "emoji import failed"})
				return
			}
			if len(emojis) != 1 {
				c.JSON(422, gin.H{"error": "missing emoji metadata"})
				return
			}
			emoji = emojis[0]
		}
		content = ":" + match[1] + "@" + *actor.Domain + ":"
	} else if !models.ValidUnicodeReaction(content) {
		c.JSON(422, gin.H{"error": "invalid reaction"})
		return
	}
	err = models.SetStatusReaction(activityDB(ctx), s.Id, actor, content, emoji, activityURI, undo)
	if errors.Is(err, models.ErrReactionForbidden) || errors.Is(err, models.ErrInvalidReaction) {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "reaction failed"})
		return
	}
	c.JSON(200, gin.H{"status": "accepted"})
}

func reactionPayload(e *models.FederationOutbox, actorURI, activityID string) (map[string]any, *models.Status, error) {
	var snap models.ReactionSnapshot
	if err := json.Unmarshal([]byte(e.Payload), &snap); err != nil {
		return nil, nil, err
	}
	var status models.Status
	err := db.GetDB().Preload("Account").Preload("ToAccounts").First(&status, e.ObjectID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && e.Kind == "ReactionUndo" {
		status.Id = e.ObjectID
		status.Account = &models.Account{Username: snap.OwnerUsername, LocalActorURI: snap.OwnerActorURI}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	if e.Kind == "Reaction" {
		var count int64
		if err := db.GetDB().Model(&models.StatusReaction{}).Where("id = ? AND account_id = ? AND status_id = ? AND "+models.ReactionNameSQL(db.GetDB())+" = ?", snap.Reaction.ID, e.AccountID, e.ObjectID, snap.Reaction.Name).Count(&count).Error; err != nil {
			return nil, nil, err
		}
		if count == 0 {
			return nil, nil, nil
		}
	}
	if e.Kind == "Reaction" && snap.Emoji != nil {
		var current models.Emoji
		if err := db.GetDB().First(&current, snap.Emoji.Id).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		} else if err != nil {
			return nil, nil, err
		}
		if current.Disabled || current.LocalOnly {
			return nil, nil, nil
		}
	}
	objectURI := snap.ObjectURI
	if objectURI == "" {
		objectURI = statusObjectURI(&status, status.Account, systemsettings.GetBaseURL())
	}
	likeID := actorURI + "/reactions/" + strconv.FormatUint(snap.Reaction.ID, 10)
	like := map[string]any{"id": likeID, "type": "Like", "actor": actorURI, "object": objectURI, "content": snap.Reaction.Name, "_misskey_reaction": snap.Reaction.Name}
	if snap.Emoji != nil && !snap.Emoji.LocalOnly {
		like["tag"] = []any{map[string]any{"type": "Emoji", "id": systemsettings.GetBaseURL() + "/emojis/" + strconv.FormatUint(snap.Emoji.Id, 10), "name": ":" + snap.Emoji.Shortcode + ":", "icon": map[string]any{"type": "Image", "mediaType": snap.Emoji.ContentType, "url": snap.Emoji.Url}}}
	}
	if e.Kind == "ReactionUndo" {
		return map[string]any{"id": activityID, "type": "Undo", "actor": actorURI, "object": like}, &status, nil
	}
	return like, &status, nil
}

// HTTP acceptance does not guarantee the peer has applied a queued Like. Do
// not send a stale queued Like after its local reaction was replaced/revoked;
// revocations themselves still go through to clean already-delivered copies.
func reactionDeliveryCurrent(auth DeliveryAuthorization, activity map[string]any) (bool, error) {
	if auth.Kind != "Reaction" {
		return true, nil
	}
	var row models.StatusReaction
	err := db.GetDB().Where("account_id = ? AND status_id = ?", auth.AccountID, auth.StatusID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	actor, _ := activity["actor"].(string)
	id, _ := activity["id"].(string)
	if actor == "" || id != actor+"/reactions/"+strconv.FormatUint(row.ID, 10) || reactionContent(activity) != row.Name {
		return false, nil
	}
	if row.EmojiID != 0 {
		var emoji models.Emoji
		err := db.GetDB().First(&emoji, row.EmojiID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if emoji.Disabled || emoji.LocalOnly {
			return false, nil
		}
	}
	return true, nil
}
