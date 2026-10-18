package serializer

import (
	"strconv"

	"github.com/kasuganosora/community/models"
)

/*
*

	{
	    "id": "110472445918843421",
	    "following": true,
	    "showing_reblogs": true,
	    "notifying": false,
	    "languages": null,
	    "followed_by": false,
	    "blocking": false,
	    "blocked_by": false,
	    "muting": false,
	    "muting_notifications": false,
	    "requested": false,
	    "requested_by": false,
	    "domain_blocking": false,
	    "endorsed": false,
	    "note": ""
	}
*/

type FollowStatus struct {
	FollowedMessage     string   `json:"followed_message,omitempty"`
	Id                  string   `json:"id"`
	Following           bool     `json:"following"`
	ShowingReblogs      bool     `json:"showing_reblogs"`
	Notifying           bool     `json:"notifying"`
	Languages           []string `json:"languages"`
	FollowedBy          bool     `json:"followed_by"`
	Blocking            bool     `json:"blocking"`
	BlockedBy           bool     `json:"blocked_by"`
	Muting              bool     `json:"muting"`
	MutingNotifications bool     `json:"muting_notifications"`
	Requested           bool     `json:"requested"`
	RequestedBy         bool     `json:"requested_by"`
	DomainBlocking      bool     `json:"domain_blocking"`
	Endorsed            bool     `json:"endorsed"`
	Note                string   `json:"note"`
}

func ToFollowStatus(account *models.Account, follow *models.Follow) (ret *FollowStatus) {
	ret = &FollowStatus{
		Id:                  strconv.FormatUint(account.Id, 10),
		Notifying:           false,
		Languages:           nil,
		FollowedBy:          false,
		Blocking:            false,
		BlockedBy:           false,
		Muting:              false,
		MutingNotifications: false,
		Requested:           false,
		RequestedBy:         false,
		DomainBlocking:      false,
		Endorsed:            false,
		Note:                "",
	}

	if follow != nil {
		ret.Following = follow.Status == models.FollowStatusFollowed
		ret.Requested = follow.Status == models.FollowStatusRequested
		if ret.Following {
			ret.FollowedMessage = account.FollowedMessage
		}

		var isReblog bool
		if follow.Properties["reblogs"] != nil {
			isReblog = follow.Properties["reblogs"].(bool) //nolint:forcetypeassert
		}

		ret.ShowingReblogs = isReblog
	}

	return
}
