package serializer

import (
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRichStatusSourceIsEditablePlainText(t *testing.T) {
	s := ToSource(&models.StatusContent{StatusId: 5, Content: `<p>First</p><p><a href="https://example.org">site</a></p>`, ContentFormat: "html"})
	require.Equal(t, "First\nsite (https://example.org)", s.Text)
	plain := ToSource(&models.StatusContent{Content: "<b>literal</b>"})
	require.Equal(t, "<b>literal</b>", plain.Text)
}
