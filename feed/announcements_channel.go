package feed

// AnnouncementsChannel is the instance-wide pub/sub channel for announcement
// changes (published, edited, withdrawn). It has no stored timeline; streams
// that drive unread badges subscribe to it.
type AnnouncementsChannel struct{}

func (AnnouncementsChannel) GetKeyFormat() string  { return "announcements:%s" }
func (AnnouncementsChannel) GetTimelineId() string { return "all" }
func (AnnouncementsChannel) Rebuild() error        { return nil }

// PushAnnouncementChanged tells connected clients to recount unread announcements.
func PushAnnouncementChanged(id uint64) error {
	return PushToClient(AnnouncementsChannel{}, "announcement", map[string]uint64{"id": id})
}
