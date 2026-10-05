package messages

import (
	"fmt"
	"sync"
	"time"

	"github.com/rivo/tview"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
)

// syncIdleTimeout is how long /relink waits for more data from the phone
// before it considers the sync complete.
const syncIdleTimeout = 30 * time.Second

// chatSync tracks the progress of /relink.
type chatSync struct {
	sm    *SessionManager
	lock  sync.Mutex
	start time.Time

	linked       bool
	chunks       int
	chats        map[string]struct{}
	messages     int
	progress     uint32
	appStateDone map[appstate.WAPatchName]struct{}
	idleTimer    *time.Timer
	finished     bool
}

// relink links whatscli again to get the chat history, which only the phone
// has and only sends to newly linked devices. It removes the login, the cache
// database and the saved chats first, so that everything comes from the phone.
func (sm *SessionManager) relink() {
	sm.uiHandler.PrintText(tview.Escape("[1/3] Unlinking whatscli and removing its stored data..."))
	// the old login would be used again if it stays
	if err := sm.removeSession(true); err != nil {
		sm.uiHandler.PrintError(err)
		return
	}

	sm.setChatSync(&chatSync{
		sm:           sm,
		start:        time.Now(),
		chats:        make(map[string]struct{}),
		appStateDone: make(map[appstate.WAPatchName]struct{}),
	})
	sm.uiHandler.PrintText(tview.Escape("[2/3] Link whatscli again: on your phone open WhatsApp > Settings > Linked devices > Link a device"))
	if err := sm.login(); err != nil {
		sm.uiHandler.PrintError(err)
		sm.setChatSync(nil)
	}
}

func (sm *SessionManager) setChatSync(cs *chatSync) {
	sm.chatSyncLock.Lock()
	defer sm.chatSyncLock.Unlock()
	sm.chatSync = cs
}

// getChatSync returns the running /relink, or nil.
func (sm *SessionManager) getChatSync() *chatSync {
	sm.chatSyncLock.Lock()
	defer sm.chatSyncLock.Unlock()
	return sm.chatSync
}

// onLinked is called when the QR code was scanned.
func (cs *chatSync) onLinked() {
	cs.lock.Lock()
	defer cs.lock.Unlock()
	cs.linked = true
	cs.print("[3/3] Linked. Waiting for your phone to send the chat history and chat settings...")
	cs.resetIdleTimerLocked()
}

// onHistory is called for every part of the chat history that arrives.
func (cs *chatSync) onHistory(data *waHistorySync.HistorySync, chatIDs []string, messages int) {
	cs.lock.Lock()
	defer cs.lock.Unlock()
	if cs.finished {
		return
	}
	cs.chunks++
	for _, id := range chatIDs {
		cs.chats[id] = struct{}{}
	}
	cs.messages += messages
	progress := ""
	if data.GetProgress() > 0 {
		cs.progress = data.GetProgress()
		progress = fmt.Sprintf(" %d%%,", data.GetProgress())
	}
	cs.print(fmt.Sprintf("Chat history:%s part %d (%s), %d chats and %d messages so far",
		progress, cs.chunks, data.GetSyncType(), len(cs.chats), cs.messages))
	cs.checkDoneLocked()
}

// onAppStateSynced is called when a part of the chat settings was synced.
func (cs *chatSync) onAppStateSynced(name appstate.WAPatchName) {
	cs.lock.Lock()
	defer cs.lock.Unlock()
	if cs.finished || !cs.linked {
		return
	}
	cs.appStateDone[name] = struct{}{}
	cs.print(fmt.Sprintf("Chat settings: %d of %d parts synced (%s)", len(cs.appStateDone), len(appstate.AllPatchNames), name))
	cs.checkDoneLocked()
}

// checkDoneLocked finishes when everything arrived, or waits for more data.
func (cs *chatSync) checkDoneLocked() {
	if cs.progress >= 100 && len(cs.appStateDone) == len(appstate.AllPatchNames) {
		cs.finishLocked()
		return
	}
	cs.resetIdleTimerLocked()
}

func (cs *chatSync) resetIdleTimerLocked() {
	if cs.idleTimer != nil {
		cs.idleTimer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(syncIdleTimeout, func() {
		cs.lock.Lock()
		defer cs.lock.Unlock()
		// data may have arrived and replaced the timer while this waited for the lock
		if cs.idleTimer == timer {
			cs.finishLocked()
		}
	})
	cs.idleTimer = timer
}

func (cs *chatSync) finishLocked() {
	if cs.finished {
		return
	}
	cs.finished = true
	if cs.idleTimer != nil {
		cs.idleTimer.Stop()
	}
	cs.sm.chatSyncLock.Lock()
	if cs.sm.chatSync == cs { // not a /relink that was started after it
		cs.sm.chatSync = nil
	}
	cs.sm.chatSyncLock.Unlock()

	shown, archived := 0, 0
	for _, chat := range cs.sm.db.GetChatIds() {
		if chat.Hidden {
			continue
		} else if chat.InArchive {
			archived++
		} else {
			shown++
		}
	}
	if cs.chunks == 0 || len(cs.appStateDone) < len(appstate.AllPatchNames) {
		cs.print(fmt.Sprintf("Sync incomplete: no new data from your phone for %s (%d history parts, %d of %d settings parts). Keep WhatsApp open on your phone and run /relink again.",
			syncIdleTimeout, cs.chunks, len(cs.appStateDone), len(appstate.AllPatchNames)))
		return
	}
	cs.print(fmt.Sprintf("Sync complete: %d chats and %d archived chats", shown, archived))
}

// print shows a progress line with the time since the sync started.
func (cs *chatSync) print(text string) {
	elapsed := time.Since(cs.start).Round(time.Second)
	cs.sm.uiHandler.PrintText(tview.Escape(fmt.Sprintf("[%s] %s", elapsed, text)))
}
