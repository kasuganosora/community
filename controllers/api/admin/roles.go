package admin

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type adminRoleBody struct {
	Name        string   `json:"name"`
	Color       string   `json:"color"`
	Highlighted bool     `json:"highlighted"`
	Permissions []string `json:"permissions"`
}

var roleColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func adminPage(c *gin.Context) (int, int) {
	limit, offset := 40, 0
	if n, e := strconv.Atoi(c.Query("limit")); e == nil && n > 0 && n <= 100 {
		limit = n
	}
	if n, e := strconv.Atoi(c.Query("offset")); e == nil && n >= 0 {
		offset = n
	}
	return limit, offset
}
func adminRoleJSON(r *models.Role) gin.H {
	return gin.H{"id": strconv.FormatInt(r.ID, 10), "name": r.Name, "color": r.Color, "highlighted": r.Highlighted, "permissions": models.AdminCapabilities(r.Permissions), "protected": r.Permissions & ^models.SupportedAdminPermissions() != 0}
}
func requireAdminCapability(c *gin.Context, conn *gorm.DB, bit uint64) (*models.User, bool) {
	raw, _ := c.Get("user")
	user, _ := raw.(*models.User)
	if user == nil {
		c.AbortWithStatusJSON(401, gin.H{"error": "authentication required"})
		return nil, false
	}
	bits, err := models.EffectiveAdminPermissions(conn, user)
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "permission lookup failed"})
		return nil, false
	}
	if bits&bit != bit {
		c.AbortWithStatusJSON(403, gin.H{"error": "permission denied"})
		return nil, false
	}
	return user, true
}
func ListRoles(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context())
	user, ok := requireAdminCapability(c, conn, models.PermissionManageRoles)
	if !ok {
		return
	}
	limit, offset := adminPage(c)
	var total int64
	var roles []models.Role
	if conn.Model(&models.Role{}).Count(&total).Error != nil || conn.Order("id ASC").Limit(limit).Offset(offset).Find(&roles).Error != nil {
		c.JSON(500, gin.H{"error": "could not load roles"})
		return
	}
	bits, err := models.EffectiveAdminPermissions(conn, user)
	if err != nil {
		c.JSON(500, gin.H{"error": "permission lookup failed"})
		return
	}
	rows := make([]gin.H, 0, len(roles))
	for n := range roles {
		rows = append(rows, adminRoleJSON(&roles[n]))
	}
	c.JSON(200, gin.H{"items": rows, "total": total, "available_permissions": models.AdminCapabilities(bits)})
}
func CreateRole(c *gin.Context) { saveRole(c, false) }
func UpdateRole(c *gin.Context) { saveRole(c, true) }
func roleMutation(c *gin.Context, run func(*gorm.DB, *models.User, uint64) error) {
	var status int
	var message string
	err := db.GetDB().WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		// Role and membership writers share this lock ordering. No stale session
		// capabilities are trusted when changing authority.
		var locks []models.Role
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id ASC").Find(&locks).Error; err != nil {
			return err
		}
		raw, _ := c.Get("user")
		actor, _ := raw.(*models.User)
		if actor == nil {
			status, message = 401, "authentication required"
			return nil
		}
		var current models.User
		if err := tx.First(&current, actor.Id).Error; err != nil {
			return err
		}
		bits, err := models.EffectiveAdminPermissions(tx, &current)
		if err != nil {
			return err
		}
		if bits&models.PermissionManageRoles == 0 {
			status, message = 403, "permission denied"
			return nil
		}
		return run(tx, &current, bits)
	})
	if err != nil {
		var validation *roleValidation
		if errors.As(err, &validation) {
			c.JSON(validation.status, gin.H{"error": validation.message})
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "role or account not found"})
		} else {
			c.JSON(500, gin.H{"error": "role operation failed"})
		}
		return
	}
	if status != 0 {
		c.JSON(status, gin.H{"error": message})
	}
}

type roleValidation struct {
	status  int
	message string
}

func (e *roleValidation) Error() string { return e.message }
func roleInvalid(message string) error  { return &roleValidation{422, message} }
func saveRole(c *gin.Context, update bool) {
	var body adminRoleBody
	if c.ShouldBindJSON(&body) != nil {
		c.JSON(400, gin.H{"error": "invalid role"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len([]rune(body.Name)) > 20 || (body.Color != "" && !roleColor.MatchString(body.Color)) {
		c.JSON(422, gin.H{"error": "role name must be 1–20 characters and color must be #RRGGBB"})
		return
	}
	var requested uint64
	for _, name := range body.Permissions {
		bit, ok := models.AdminCapabilityBits[name]
		if !ok {
			c.JSON(422, gin.H{"error": "unsupported permission"})
			return
		}
		requested |= bit
	}
	var id uint64
	if update {
		var ok bool
		id, ok = adminAccountID(c)
		if !ok {
			return
		}
	}
	roleMutation(c, func(tx *gorm.DB, actor *models.User, bits uint64) error {
		if requested & ^bits != 0 {
			return roleInvalid("cannot grant permissions you do not possess")
		}
		target := models.Role{}
		if update {
			if err := tx.First(&target, id).Error; err != nil {
				return err
			}
			if target.Permissions & ^models.SupportedAdminPermissions() != 0 {
				return roleInvalid("reserved roles cannot be modified")
			}
			if target.Permissions & ^bits != 0 {
				return roleInvalid("cannot modify a more privileged role")
			}
		}
		var duplicate int64
		if err := tx.Model(&models.Role{}).Where("name = ? AND id <> ?", body.Name, target.ID).Count(&duplicate).Error; err != nil {
			return err
		}
		if duplicate > 0 {
			return roleInvalid("role name already exists")
		}
		target.Name, target.Color, target.Highlighted, target.Permissions = body.Name, body.Color, body.Highlighted, requested
		if err := tx.Save(&target).Error; err != nil {
			return err
		}
		// Emit only after transaction outcome, via the response value in context.
		c.Set("admin_role_result", adminRoleJSON(&target))
		return nil
	})
	if !c.Writer.Written() {
		if result, ok := c.Get("admin_role_result"); ok {
			c.JSON(http.StatusOK, result)
		}
	}
}
func DeleteRole(c *gin.Context) {
	id, ok := adminAccountID(c)
	if !ok {
		return
	}
	roleMutation(c, func(tx *gorm.DB, actor *models.User, bits uint64) error {
		var target models.Role
		if err := tx.First(&target, id).Error; err != nil {
			return err
		}
		if target.Permissions & ^models.SupportedAdminPermissions() != 0 || target.Permissions & ^bits != 0 {
			return roleInvalid("reserved or more privileged roles cannot be deleted")
		}
		if err := tx.Where("role_id = ?", id).Delete(&models.RoleUser{}).Error; err != nil {
			return err
		}
		return tx.Delete(&target).Error
	})
	if !c.Writer.Written() {
		c.JSON(200, gin.H{})
	}
}
func ListRoleMembers(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context())
	if _, ok := requireAdminCapability(c, conn, models.PermissionManageRoles); !ok {
		return
	}
	id, ok := adminAccountID(c)
	if !ok {
		return
	}
	var role models.Role
	if err := conn.First(&role, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "role not found"})
		return
	}
	limit, offset := adminPage(c)
	query := conn.Model(&models.Account{}).Where("domain IS NULL AND user_id IN (?)", conn.Model(&models.RoleUser{}).Select("user_id").Where("role_id = ?", id))
	var total int64
	var accounts []models.Account
	if query.Count(&total).Error != nil || query.Order("id ASC").Limit(limit).Offset(offset).Find(&accounts).Error != nil {
		c.JSON(500, gin.H{"error": "could not load role members"})
		return
	}
	rows := []gin.H{}
	for _, a := range accounts {
		rows = append(rows, gin.H{"account_id": strconv.FormatUint(a.Id, 10), "username": a.Username})
	}
	c.JSON(200, gin.H{"items": rows, "total": total})
}
func AssignAdminRole(c *gin.Context)   { changeRoleMember(c, true) }
func UnassignAdminRole(c *gin.Context) { changeRoleMember(c, false) }
func changeRoleMember(c *gin.Context, assign bool) {
	id, ok := adminAccountID(c)
	if !ok {
		return
	}
	accountID, e := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if e != nil || accountID == 0 {
		c.JSON(400, gin.H{"error": "invalid account id"})
		return
	}
	roleMutation(c, func(tx *gorm.DB, actor *models.User, bits uint64) error {
		var role models.Role
		if err := tx.First(&role, id).Error; err != nil {
			return err
		}
		if role.Permissions & ^models.SupportedAdminPermissions() != 0 || role.Permissions & ^bits != 0 {
			return roleInvalid("cannot assign reserved or more privileged roles")
		}
		var account models.Account
		if err := tx.Where("domain IS NULL AND user_id > 0").First(&account, accountID).Error; err != nil {
			return err
		}
		if account.UserId == actor.Id {
			return roleInvalid("cannot change your own role assignments")
		}
		var target models.User
		if err := tx.First(&target, account.UserId).Error; err != nil {
			return err
		}
		if target.IsAdmin && !actor.IsAdmin {
			return roleInvalid("only a root administrator may change root assignments")
		}
		if assign {
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.RoleUser{RoleID: id, UserID: target.Id}).Error
		}
		return tx.Where("role_id = ? AND user_id = ?", id, target.Id).Delete(&models.RoleUser{}).Error
	})
	if !c.Writer.Written() {
		c.JSON(200, gin.H{})
	}
}
