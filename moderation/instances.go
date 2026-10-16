package moderation

import (
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var suspended struct {
	sync.Mutex
	set     map[string]bool
	builtAt time.Time
}

// InvalidateSuspended drops the cached suspended-instance set.
func InvalidateSuspended() {
	suspended.Lock()
	suspended.set = nil
	suspended.Unlock()
}

// IsSuspendedInstance reports whether deliveries to host are manually
// suspended (instance_metadata.suspension_state). Exact host match, like
// Misskey's per-instance row. Errors and missing columns count as not
// suspended.
func IsSuspendedInstance(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	suspended.Lock()
	defer suspended.Unlock()
	if suspended.set == nil || time.Since(suspended.builtAt) > cacheTTL {
		set := map[string]bool{}
		if conn := db.GetDB(); conn != nil {
			var domains []string
			if conn.Model(&models.InstanceMetadata{}).Where("suspension_state <> ?", models.InstanceSuspensionNone).Pluck("domain", &domains).Error == nil {
				for _, d := range domains {
					set[strings.ToLower(d)] = true
				}
			}
		}
		suspended.set, suspended.builtAt = set, time.Now()
	}
	return suspended.set[host]
}

// SetInstanceSuspended records the manual suspension state of host,
// creating its instance_metadata row when needed.
func SetInstanceSuspended(conn *gorm.DB, host string, on bool) error {
	host = NormalizeHost(host)
	state := models.InstanceSuspensionNone
	if on {
		state = models.InstanceSuspensionManual
	}
	row := models.InstanceMetadata{Domain: host, SuspensionState: state}
	err := conn.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "domain"}}, DoUpdates: clause.AssignmentColumns([]string{"suspension_state"})}).Create(&row).Error
	InvalidateSuspended()
	return err
}

// DeliveryAllowed reports whether an outbound request to host may be made:
// not blocked (moderation list or suspending domain block, subdomains
// included) and not a manually suspended instance.
func DeliveryAllowed(host string) bool {
	host = strings.ToLower(host)
	if i := strings.LastIndexByte(host, ':'); i > 0 && !strings.Contains(host[i:], "]") {
		if Current().IsBlockedHost(host) || IsSuspendedInstance(host) {
			return false
		}
		host = host[:i]
	}
	if Current().IsBlockedHost(host) || IsSuspendedInstance(host) {
		return false
	}
	suspend, _, err := models.DomainSeverities(db.GetDB())
	return err != nil || !suspend.Contains(host)
}
