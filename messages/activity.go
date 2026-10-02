package messages

import (
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// activityLogger passes the log output of whatsmeow on, and notes when it
// received data from WhatsApp: whatsmeow logs everything it receives to its
// "Recv" logger, including the answers to the keepalive pings it sends every
// 20 to 30 seconds, so this shows whether the connection still works.
type activityLogger struct {
	waLog.Logger
	// called when data was received
	received func()
	// whether this is the "Recv" logger, whose debug output is what was received
	isRecv bool
}

func (l activityLogger) Sub(module string) waLog.Logger {
	return activityLogger{Logger: l.Logger.Sub(module), received: l.received, isRecv: module == "Recv"}
}

func (l activityLogger) Debugf(msg string, args ...any) {
	if l.isRecv {
		l.received()
	}
	l.Logger.Debugf(msg, args...)
}

// noteReceived remembers that data was received from WhatsApp just now.
func (sm *SessionManager) noteReceived() {
	sm.lastReceived.Store(time.Now().UnixNano())
}

// LastReceived returns when data was last received from WhatsApp, or the zero
// time if nothing was received yet.
func (sm *SessionManager) LastReceived() time.Time {
	if nanos := sm.lastReceived.Load(); nanos != 0 {
		return time.Unix(0, nanos)
	}
	return time.Time{}
}

// logDebug writes to the log, if there is one, see SessionManager.Log
func (sm *SessionManager) logDebug(format string, args ...any) {
	if sm.Log != nil {
		sm.Log.Debugf(format, args...)
	}
}

// logWarn writes a warning to the log, if there is one
func (sm *SessionManager) logWarn(format string, args ...any) {
	if sm.Log != nil {
		sm.Log.Warnf(format, args...)
	}
}
