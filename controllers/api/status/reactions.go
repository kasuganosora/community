package status

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func AddReaction(c *gin.Context)    { mutateReaction(c, false) }
func RemoveReaction(c *gin.Context) { mutateReaction(c, true) }
func mutateReaction(c *gin.Context, remove bool) {
	s := getRealStatus(c)
	actor := controllers.MustGetAccount(c)
	name := c.Param("name")
	var emoji *models.Emoji
	var err error
	// A hidden status is 404; 403 below is kept for a visible status whose
	// author does not accept this reaction.
	abortUnlessVisible(c, db.GetDB(), s, actor)
	if !remove {
		emoji, err = models.ResolveReaction(db.GetDB(), name, actor)
		if err != nil {
			c.AbortWithStatusJSON(422, gin.H{"error": "reaction is unavailable"})
			return
		}
	}
	err = models.SetStatusReaction(db.GetDB(), s.Id, actor, name, emoji, "", remove)
	if errors.Is(err, models.ErrReactionForbidden) {
		c.AbortWithStatusJSON(403, gin.H{"error": "forbidden"})
		return
	}
	if errors.Is(err, models.ErrInvalidReaction) {
		c.AbortWithStatusJSON(422, gin.H{"error": "reaction is unavailable"})
		return
	}
	controllers.AbortIfError(c, err)
	returnFullSingleStatus(c, s, actor)
}
func Reactions(c *gin.Context) {
	s := getRealStatus(c)
	var actor *models.Account
	if a, ok := c.Get("account"); ok {
		actor, _ = a.(*models.Account)
	}
	abortUnlessVisible(c, db.GetDB(), s, actor)
	result, err := serializer.StatusReactions(s.Id, actor, true)
	controllers.AbortIfError(c, err)
	c.JSON(200, result)
}
