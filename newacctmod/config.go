package newacctmod

import (
	"strings"

	"github.com/kasuganosora/community/systemsettings"
)

const (
	KeyEnabled       = "config.new_account_mod.enabled"
	KeyObservationN  = "config.new_account_mod.observation_n"
	KeyContextX      = "config.new_account_mod.context_x"
	KeyPolicyText    = "config.new_account_mod.policy_text"
	KeyHoldFederate  = "config.new_account_mod.hold_federation" // default true
)

const (
	defaultObservationN = 20
	defaultContextX     = 8
)

func Enabled() bool {
	return systemsettings.GetBool(KeyEnabled, true)
}

func ObservationN() int {
	n := systemsettings.GetInt(KeyObservationN, defaultObservationN)
	if n < 1 {
		return defaultObservationN
	}
	return n
}

func ContextX() int {
	n := systemsettings.GetInt(KeyContextX, defaultContextX)
	if n < 1 {
		return defaultContextX
	}
	return n
}

func PolicyText() string {
	return strings.TrimSpace(systemsettings.GetString(KeyPolicyText, defaultPolicy))
}

func HoldFederation() bool {
	return systemsettings.GetBool(KeyHoldFederate, true)
}

const defaultPolicy = `Ban clear advertising spam farms: identical promo copy across accounts, lottery/outlet scam links, mass hashtag spam, and accounts created only to push external shops. Allow normal personal posts, questions, and non-commercial discussion. When unsure, flag for human review rather than suspend.`
