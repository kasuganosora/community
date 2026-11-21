package serializer

// NotificationGroups is the Mastodon v2 notification page. Accounts and
// statuses are shared across the groups that refer to them.
type NotificationGroups struct {
	Accounts           []*Account          `json:"accounts"`
	PartialAccounts    *[]PartialAccount   `json:"partial_accounts,omitempty"`
	Statuses           []*Status           `json:"statuses"`
	NotificationGroups []NotificationGroup `json:"notification_groups"`
}

type PartialAccount struct {
	Id                string `json:"id"`
	Acct              string `json:"acct"`
	Locked            bool   `json:"locked"`
	Bot               bool   `json:"bot"`
	Url               string `json:"url"`
	Avatar            string `json:"avatar"`
	AvatarStatic      string `json:"avatar_static"`
	AvatarDescription string `json:"avatar_description"`
}

type NotificationGroup struct {
	GroupKey                 string    `json:"group_key"`
	NotificationsCount       int       `json:"notifications_count"`
	Type                     string    `json:"type"`
	MostRecentNotificationId string    `json:"most_recent_notification_id"`
	PageMinId                string    `json:"page_min_id"`
	PageMaxId                string    `json:"page_max_id"`
	LatestPageNotificationAt *APITime  `json:"latest_page_notification_at"`
	SampleAccountIds         []string  `json:"sample_account_ids"`
	StatusId                 string    `json:"status_id,omitempty"`
	Report                   *Report   `json:"report,omitempty"`
	EmojiReaction            *Reaction `json:"emoji_reaction,omitempty"`
	List                     *List     `json:"list,omitempty"`
}

// ToNotificationGroups uses one group per visible notification. Keeping the
// existing page boundaries and IDs lets either API version page the same inbox.
func ToNotificationGroups(notifications []*Notification, partialAvatars bool) NotificationGroups {
	out := NotificationGroups{
		Accounts:           []*Account{},
		Statuses:           []*Status{},
		NotificationGroups: make([]NotificationGroup, 0, len(notifications)),
	}
	if partialAvatars {
		// Each group has one sender, which is always a full account.
		out.PartialAccounts = &[]PartialAccount{}
	}
	accounts := make(map[string]bool, len(notifications))
	statuses := make(map[string]bool, len(notifications))
	for _, n := range notifications {
		id := n.Account.Id // The notification serializer omits rows without a visible sender.
		if !accounts[id] {
			accounts[id] = true
			out.Accounts = append(out.Accounts, n.Account)
		}
		group := NotificationGroup{
			GroupKey: n.GroupKey, NotificationsCount: 1, Type: n.Type,
			MostRecentNotificationId: n.Id, PageMinId: n.Id, PageMaxId: n.Id,
			LatestPageNotificationAt: n.CreatedAt, SampleAccountIds: []string{id},
			Report: n.Report, EmojiReaction: n.EmojiReaction, List: n.List,
		}
		if n.Status != nil {
			group.StatusId = n.Status.Id
			if !statuses[n.Status.Id] {
				statuses[n.Status.Id] = true
				out.Statuses = append(out.Statuses, n.Status)
			}
		}
		out.NotificationGroups = append(out.NotificationGroups, group)
	}
	return out
}
