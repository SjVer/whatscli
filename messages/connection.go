package messages

import (
	"sync"
	"time"
)

// connectionLostDelay is how long the connection must stay lost before that is
// notified: whatsmeow reconnects by itself, usually within seconds.
var connectionLostDelay = 30 * time.Second

const connectionLostText = "Connection to WhatsApp lost, reconnecting..."

// connectionWatch notifies when the connection to WhatsApp is lost, once per
// outage, and when it is back after that.
type connectionWatch struct {
	lock     sync.Mutex
	timer    *time.Timer
	notified bool
}

// disconnected is called when the connection closed, which is notified when
// it isn't back soon.
func (w *connectionWatch) disconnected() {
	w.lock.Lock()
	defer w.lock.Unlock()
	if w.timer == nil && !w.notified {
		w.timer = time.AfterFunc(connectionLostDelay, func() {
			w.lock.Lock()
			defer w.lock.Unlock()
			if w.timer != nil {
				w.timer = nil
				w.notifyLocked(connectionLostText)
			}
		})
	}
}

// unresponsive is called when WhatsApp stopped answering: there was no answer
// to the pings of the last half minute, so that is notified right away.
func (w *connectionWatch) unresponsive() {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.stopTimerLocked()
	w.notifyLocked(connectionLostText)
}

// loggedOut is called when the phone logged whatscli out.
func (w *connectionWatch) loggedOut(reason string) {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.stopTimerLocked()
	w.notified = false
	w.notifyLocked("Logged out: " + reason)
}

// connected is called when the connection works again.
func (w *connectionWatch) connected() {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.stopTimerLocked()
	if w.notified {
		w.notified = false
		go sendNotification(appName, "Connected to WhatsApp again", "")
	}
}

func (w *connectionWatch) stopTimerLocked() {
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}

// notifyLocked notifies text, once until the connection is back. Requires lock.
func (w *connectionWatch) notifyLocked(text string) {
	if !w.notified {
		w.notified = true
		go sendNotification(appName, text, "")
	}
}
