package main

import (
	_ "embed"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/normen/whatscli/messages"
	"github.com/skratchdot/open-golang/open"
)

type UiHandler struct{}

func (u UiHandler) NewMessage(msg messages.Message) {
	onUI(func() { showNewMessage(msg) })
}

// latestUpdate queues an update of the UI with a value, like the chat list,
// which can change many times a second, e.g. while receiving messages: it is
// only shown once for the changes until it is drawn, with the newest value.
type latestUpdate[T any] struct {
	lock   sync.Mutex
	value  T
	queued bool
}

// queue shows value with show, on the goroutine of the UI
func (update *latestUpdate[T]) queue(value T, show func(T)) {
	update.lock.Lock()
	update.value = value
	queued := update.queued
	update.queued = true
	update.lock.Unlock()
	if queued {
		return
	}
	onUI(func() {
		update.lock.Lock()
		value := update.value
		update.queued = false
		update.lock.Unlock()
		show(value)
	})
}

// uiQueue are the functions that run on the goroutine of the UI next, see onUI
var uiQueue struct {
	lock     sync.Mutex
	funcs    []func()
	draining bool
}

// onUI runs f on the goroutine of the UI, without waiting for it, after the
// functions that were queued before it: updates of the session manager are
// shown in the order they were made. Without an app, e.g. in tests, it runs
// right away.
func onUI(f func()) {
	if app == nil {
		f()
		return
	}
	uiQueue.lock.Lock()
	uiQueue.funcs = append(uiQueue.funcs, f)
	draining := uiQueue.draining
	uiQueue.draining = true
	uiQueue.lock.Unlock()
	if draining {
		return
	}
	go func() {
		for {
			uiQueue.lock.Lock()
			if len(uiQueue.funcs) == 0 {
				uiQueue.draining = false
				uiQueue.lock.Unlock()
				return
			}
			next := uiQueue.funcs[0]
			uiQueue.funcs = uiQueue.funcs[1:]
			uiQueue.lock.Unlock()
			app.QueueUpdateDraw(next)
		}
	}()
}

// queuedWriter writes to the message panel from other goroutines, see onUI
type queuedWriter struct{}

func (queuedWriter) Write(data []byte) (int, error) {
	text := string(data)
	onUI(func() {
		printedSinceRender.Store(true)
		fmt.Fprint(textView, text)
	})
	return len(data), nil
}

// NewScreen shows the messages of the open chat again, e.g. with the receipts
// in a group, see latestUpdate
func (u UiHandler) NewScreen(chatID string, msgs []messages.Message) {
	screenUpdate.queue(chatScreen{chatID, msgs}, func(screen chatScreen) {
		// queued before another chat was opened
		if screen.chatID != currentReceiver.Id {
			return
		}
		chatMessages = screen.msgs
		renderMessages()
	})
}

// chatScreen are the messages of a chat to show, see NewScreen
type chatScreen struct {
	chatID string
	msgs   []messages.Message
}

// SetChats shows the chat list again, see latestUpdate
func (u UiHandler) SetChats(chats []messages.Chat) {
	chatsUpdate.queue(chats, func(chats []messages.Chat) {
		allChats = chats
		// renderChats updates currentReceiver with the chat from the new list
		reactions := currentReceiver.UnreadReactions
		renderChats()
		// the new reactions are shown in the chat
		if !slices.Equal(reactions, currentReceiver.UnreadReactions) && !printedSinceRender.Load() {
			renderMessages()
		}
	})
}

// CloseChat goes back to the chat list when the chat is open, e.g. after it was archived
func (u UiHandler) CloseChat(chatID string) {
	onUI(func() {
		if currentReceiver.Id == chatID {
			showChatsRoot()
			app.SetFocus(treeView)
		}
	})
}

// SetNotice shows a dim status line in a chat, see UiMessageHandler
func (u UiHandler) SetNotice(chatID, key, text string) {
	onUI(func() { showNotice(chatID, key, text) })
}

// OpenFile opens a file or URL with its default app, without waiting for it
func (u UiHandler) OpenFile(target string) {
	if err := open.Start(target); err != nil {
		onUI(func() { PrintErrorMsg("failed to open "+target+":", err) })
	}
}

func (u UiHandler) SetStatus(status messages.SessionStatus) {
	onUI(func() { UpdateStatusBar(status) })
}

// GetWriter returns a writer to the message panel, e.g. for the QR code
func (u UiHandler) GetWriter() io.Writer {
	return queuedWriter{}
}

// the messages and chat list to show next, see NewScreen and SetChats
var (
	screenUpdate latestUpdate[chatScreen]
	chatsUpdate  latestUpdate[[]messages.Chat]
)

func (u UiHandler) PrintError(err error) {
	onUI(func() { PrintError(err) })
}

// PrintText prints a status message of the session manager, dim like the
// hints, so that it isn't taken for a message
func (u UiHandler) PrintText(msg string) {
	onUI(func() { PrintText("[::d]" + msg + "[::-]") })
}
