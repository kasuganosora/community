package streaming

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
)

func TestVisitorStreamAllowed(t *testing.T) {
	user := &models.Account{}
	cases := []struct {
		mode, stream string
		want         bool
	}{
		{moderation.UGCAll, "public", true},
		{moderation.UGCLocal, "public", false},
		{moderation.UGCLocal, "public:remote", false},
		{moderation.UGCLocal, "public:media", false},
		{moderation.UGCLocal, "public:local", true},
		{moderation.UGCLocal, "public:local:media", true},
		{moderation.UGCLocal, "hashtag", false},
		{moderation.UGCLocal, "hashtag:local", true},
		{moderation.UGCNone, "public:local", false},
		{moderation.UGCNone, "hashtag:local", false},
	}
	for _, tc := range cases {
		restore := moderation.SetForTesting(moderation.Snapshot{UGCVisibilityForVisitor: tc.mode})
		if got := visitorStreamAllowed(nil, tc.stream); got != tc.want {
			t.Errorf("%s %s: got %v want %v", tc.mode, tc.stream, got, tc.want)
		}
		if !visitorStreamAllowed(user, tc.stream) {
			t.Errorf("signed-in user must not be restricted (%s %s)", tc.mode, tc.stream)
		}
		if _, err := getChannelName(nil, &StreamingRequest{Stream: tc.stream, Tag: "x"}); (err == nil) != tc.want {
			t.Errorf("getChannelName %s %s err=%v", tc.mode, tc.stream, err)
		}
		restore()
	}
}

func TestVisitorCanSeeStatuses(t *testing.T) {
	remote := "remote.example"
	local := &models.Status{Account: &models.Account{}}
	rem := &models.Status{Account: &models.Account{Domain: &remote}}
	boostOfRemote := &models.Status{Account: &models.Account{}, Reblog: rem}
	restore := moderation.SetForTesting(moderation.Snapshot{UGCVisibilityForVisitor: moderation.UGCLocal})
	defer restore()
	if !visitorCanSeeStatuses(models.Statuses{local}) {
		t.Fatal("local status hidden")
	}
	if visitorCanSeeStatuses(models.Statuses{rem}) || visitorCanSeeStatuses(models.Statuses{boostOfRemote}) {
		t.Fatal("remote content leaked to visitor in local mode")
	}
}
