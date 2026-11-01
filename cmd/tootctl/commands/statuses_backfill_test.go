package commands

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepairReplyParentsHelpExplainsCursor(t *testing.T) {
	out, err := runStatuses("repair-reply-parents", "-h")
	require.NoError(t, err)
	require.Contains(t, out, "--after-id")
	require.Contains(t, out, "--limit")
	require.Contains(t, out, "--max-pages")
	require.Contains(t, out, "older than the oldest id")
	require.Contains(t, out, "Do not raise --limit")
	require.Contains(t, out, "--after-id set to the printed oldest id")
}
