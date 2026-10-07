package ui

import (
	_ "embed"
	"fmt"

	"github.com/rivo/tview"
)

// notice is a status line shown below the messages of a chat, see SetNotice
type notice struct {
	key  string
	text string
}

// notices are the status lines of each chat, by chat ID, "" for the main
// screen, in the order they were first shown
var notices = map[string][]notice{}

// setNotice sets the notice of a chat with the key, or removes it when text is
// empty, and returns whether that changed anything
func setNotice(chatID, key, text string) bool {
	chatNotices := notices[chatID]
	for idx, current := range chatNotices {
		if current.key != key {
			continue
		}
		if current.text == text {
			return false
		} else if text == "" {
			notices[chatID] = append(chatNotices[:idx:idx], chatNotices[idx+1:]...)
		} else {
			chatNotices[idx].text = text
		}
		return true
	}
	if text == "" {
		return false
	}
	notices[chatID] = append(chatNotices, notice{key, text})
	return true
}

// noticeLine returns a notice as it is shown, dim on its own line
func noticeLine(text string) string {
	return "[::d]" + tview.Escape(text) + "[::-]\n"
}

// printNotices shows the notices of a chat dim, below an empty line
func printNotices(chatID string) {
	if len(notices[chatID]) == 0 {
		return
	}
	out := "\n"
	for _, notice := range notices[chatID] {
		out += noticeLine(notice.text)
	}
	fmt.Fprint(textView, out)
}

// showNotice sets a notice of a chat, see setNotice, and shows it if the chat
// is open. Text printed below the messages is kept, see printedSinceRender.
func showNotice(chatID, key, text string) {
	if !setNotice(chatID, key, text) || chatID != currentReceiver.Id {
		return
	}
	if !printedSinceRender.Load() {
		renderMessages()
	} else if text != "" {
		fmt.Fprint(textView, noticeLine(text))
	}
}
