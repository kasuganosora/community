package utils

import (
	"fmt"
	"strings"
)

// ParseMatrixUserID parses a Matrix user ID (@user:server) into localpart and server.
func ParseMatrixUserID(userID string) (localpart, server string, ok bool) {
	if !strings.HasPrefix(userID, "@") {
		return "", "", false
	}

	parts := strings.SplitN(userID[1:], ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}

	return parts[0], parts[1], true
}

// FormatMatrixUserID formats a Matrix user ID from localpart and server.
func FormatMatrixUserID(localpart, server string) string {
	return fmt.Sprintf("@%s:%s", localpart, server)
}

// ParseMatrixRoomID parses a Matrix room ID (!room:server) into localpart and server.
func ParseMatrixRoomID(roomID string) (localpart, server string, ok bool) {
	if !strings.HasPrefix(roomID, "!") {
		return "", "", false
	}

	parts := strings.SplitN(roomID[1:], ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}

	return parts[0], parts[1], true
}
