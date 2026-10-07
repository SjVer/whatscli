package ui

import (
	"sync/atomic"

	"github.com/gdamore/tcell/v2"
)

// Whether the user is looking at the open chat, so that the messages that
// arrive in it are read, see messages.SessionManager.ChatSeen: whatscli has
// focus, and its message panel or input does, not the chat list.

// windowFocused is whether the terminal of whatscli has focus. Terminals that
// don't report it count as always focused.
var windowFocused atomic.Bool

// chatListFocused is whether the chat list has focus, set after each draw, as
// focus changes are drawn
var chatListFocused atomic.Bool

func init() {
	windowFocused.Store(true)
}

// chatSeen returns whether the user is looking at the open chat
func chatSeen() bool {
	return windowFocused.Load() && !chatListFocused.Load()
}

// focusScreen notes when the terminal gains and loses focus, which the
// application doesn't pass on
type focusScreen struct {
	tcell.Screen
}

func (s focusScreen) PollEvent() tcell.Event {
	event := s.Screen.PollEvent()
	if focus, ok := event.(*tcell.EventFocus); ok {
		windowFocused.Store(focus.Focused)
	}
	return event
}

// newFocusScreen returns the screen for the application, which reports focus changes
func newFocusScreen() (tcell.Screen, error) {
	screen, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	return focusScreen{screen}, nil
}
