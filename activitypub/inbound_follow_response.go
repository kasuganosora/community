package activitypub

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func handleFollowResponse(ctx context.Context, c *gin.Context, activity map[string]any, target *models.Account, accepted bool) {
	actor, _ := ctx.Value(actorKey).(string)
	id, _ := activity["object"].(string)
	object, _ := activity["object"].(map[string]any)
	if object != nil {
		id, _ = object["id"].(string)
		if object["type"] != "Follow" {
			c.JSON(400, gin.H{"error": "expected Follow"})
			return
		}
	}
	// Relay subscriptions are matched by our Follow id first: the relay
	// actor is normally not a cached account.
	if handleRelayFollowResponse(ctx, c, id, object, actor, accepted) {
		return
	}
	var remote models.Account
	if err := activityDB(ctx).Where("origin_id = ? AND domain IS NOT NULL", actor).First(&remote).Error; err != nil {
		c.JSON(403, gin.H{"error": "unknown follow actor"})
		return
	}
	var follow models.Follow
	conn := activityDB(ctx)
	err := conn.Clauses(clause.Locking{Strength: "UPDATE"}).Where("follow_id = ? AND "+dialect.JSONText(conn, "properties", "activity_uri")+" = ?", remote.Id, id).First(&follow).Error
	if err == nil {
		var local models.Account
		if activityDB(ctx).First(&local, follow.AccountId).Error != nil || local.Domain != nil || (target != nil && target.Id != local.Id) {
			c.JSON(403, gin.H{"error": "wrong follow recipient"})
			return
		}
		target = &local
		if object != nil && (object["actor"] != target.ActorURI(systemsettings.GetBaseURL()) || object["object"] != actor) {
			c.JSON(403, gin.H{"error": "follow identity mismatch"})
			return
		}
	} else {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(500, gin.H{"error": "failed to locate follow"})
			return
		}
		index := strings.LastIndex(id, "/follows/")
		if index < 0 {
			handleSpecialFollowResponse(ctx, c, id, actor, accepted)
			return
		}
		localURI := id[:index]
		followID, parseErr := strconv.ParseUint(id[index+len("/follows/"):], 10, 64)
		if parseErr != nil || followID == 0 {
			c.JSON(403, gin.H{"error": "invalid follow reference"})
			return
		}
		if object != nil && (object["actor"] != localURI || object["object"] != actor) {
			c.JSON(403, gin.H{"error": "follow identity mismatch"})
			return
		}
		if target == nil {
			target = &models.Account{}
			if localActorQuery(activityDB(ctx), localURI).First(target).Error != nil {
				c.JSON(403, gin.H{"error": "unknown local actor"})
				return
			}
		}
		if target.Domain != nil || target.ActorURI(systemsettings.GetBaseURL()) != localURI {
			c.JSON(403, gin.H{"error": "wrong follow recipient"})
			return
		}
		err = activityDB(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND account_id = ? AND follow_id = ?", followID, target.Id, remote.Id).First(&follow).Error
		imported, _ := follow.Properties["activity_uri"].(string)
		if err != nil || (imported != "" && imported != id) {
			c.JSON(403, gin.H{"error": "unknown follow reference"})
			return
		}
	}
	if follow.Status != models.FollowStatusRequested {
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}
	if accepted {
		err = activityDB(ctx).Model(&follow).Update("status", models.FollowStatusFollowed).Error
		if err == nil {
			err = models.BumpAccountCounter(activityDB(ctx), target.Id, "following_count", 1)
		}
		if err == nil {
			err = models.BumpAccountCounter(activityDB(ctx), remote.Id, "followers_count", 1)
		}
	} else {
		err = activityDB(ctx).Delete(&follow).Error
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update follow"})
		return
	}
	c.JSON(200, gin.H{"status": "accepted"})
}

func handleSpecialFollowResponse(ctx context.Context, c *gin.Context, id, actor string, accepted bool) {
	if id == "" {
		c.JSON(400, gin.H{"error": "missing follow reference"})
		return
	}
	tx := activityDB(ctx)
	var proxy models.ProxyFollow
	if tx.Where("follow_activity_id = ?", id).First(&proxy).Error == nil {
		var remote models.Account
		if tx.First(&remote, proxy.AccountID).Error != nil || remote.OriginId != actor {
			c.JSON(403, gin.H{"error": "wrong proxy actor"})
			return
		}
		var err error
		if accepted {
			err = (&ProxyService{db: tx}).HandleAccept(id)
		} else {
			err = tx.Model(&proxy).Update("status", models.ProxyFollowStatusRejected).Error
		}
		if err != nil {
			c.JSON(500, gin.H{"error": "failed proxy update"})
			return
		}
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}
	c.JSON(403, gin.H{"error": "unknown follow reference"})
}
