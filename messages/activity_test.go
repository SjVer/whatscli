package messages

import (
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

func TestActivityLoggerNotesReceivedData(t *testing.T) {
	sm := &SessionManager{}
	logger := activityLogger{Logger: waLog.Noop, received: sm.noteReceived}
	logger.Sub("Send").Debugf("sent")
	logger.Debugf("something else")
	if !sm.LastReceived().IsZero() {
		t.Fatal("expected only received data to count")
	}
	logger.Sub("Recv").Debugf("<iq type=\"result\"/>")
	if since := time.Since(sm.LastReceived()); since < 0 || since > time.Second {
		t.Fatalf("expected received data to be noted just now, got %v ago", since)
	}
}
