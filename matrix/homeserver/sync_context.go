package homeserver

import (
	"context"

	"github.com/kasuganosora/community/db"
	"gorm.io/gorm"
)

type syncJoinKey struct {
	roomID, userID string
	since          int64
}

func (s *SyncService) syncDB() *gorm.DB {
	if s.connection != nil {
		return s.connection
	}
	return db.GetDB()
}

// SyncContext gives each request its own GORM session. No mutable request state
// is stored on the shared service and cancellation reaches SQL between polls.
func (s *SyncService) SyncContext(ctx context.Context, userID, deviceID string, req *SyncRequest) (*SyncResponse, *MatrixError) {
	if err := ctx.Err(); err != nil {
		return nil, &MatrixError{ErrCode: "M_UNKNOWN", Error: err.Error()}
	}
	requestService := *s
	requestService.connection = db.GetDB().WithContext(ctx)
	requestService.joinTransitions = make(map[syncJoinKey]bool)
	requestService.readError = nil
	response, matrixErr := requestService.sync(userID, deviceID, req)
	if err := ctx.Err(); err != nil {
		return nil, &MatrixError{ErrCode: "M_UNKNOWN", Error: err.Error()}
	}
	return response, matrixErr
}
func (s *SyncService) Sync(userID, deviceID string, req *SyncRequest) (*SyncResponse, *MatrixError) {
	return s.SyncContext(context.Background(), userID, deviceID, req)
}

func joinedRoomHasChanges(roomID string, room *JoinedRoom, since int64) bool {
	if len(room.Timeline.Events) > 0 || len(room.State.Events) > 0 || len(room.AccountData.Events) > 0 || getLastTypingChange(roomID) > since {
		return true
	}
	if room.Ephemeral != nil {
		for _, event := range room.Ephemeral.Events {
			if event.Type != "m.typing" {
				return true
			}
		}
	}
	return false
}

func (s *SyncService) noteReadError(err error) {
	if err != nil && s.readError == nil {
		s.readError = err
	}
}
func (s *SyncService) clientEvents(events []MatrixEvent, deviceID string) []*ClientEvent {
	result, err := batchClientEvents(s.syncDB(), events, deviceID)
	s.noteReadError(err)
	return result
}
