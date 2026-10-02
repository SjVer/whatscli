package main

import (
	"strings"

	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

// Ticks show how far the user's messages got, like on the phone: ✓ sent,
// grey ✓✓ delivered, and coloured ✓✓ read. They are at the right of the last
// line of a message, for which its text is wrapped to leave room for them.

// ticksWidth is how wide the ticks are
const ticksWidth = 2

// renderedWidth is the width of the message panel the messages were last
// shown for, as the ticks are placed for it
var renderedWidth int

// statusTicks returns the ticks of a message of the user, which is at least
// sent, see messages.Message.Status
func statusTicks(status messages.MessageStatus) string {
	switch status {
	case messages.StatusSent:
		return "[gray::-] ✓[-::-]"
	case messages.StatusDelivered:
		return "[gray::-]✓✓[-::-]"
	case messages.StatusRead:
		return "[" + config.Config.Colors.ReadMarker + "::-]✓✓[-::-]"
	}
	return ""
}

// withTicks wraps the tagged text of a message to the width, leaving room for
// its ticks at the right of its last line
func withTicks(text string, ticks string, width int) string {
	if ticks == "" || width <= ticksWidth+10 {
		return text
	}
	lines := tview.WordWrap(text, width-ticksWidth-1)
	if len(lines) == 0 {
		lines = []string{""}
	}
	last := len(lines) - 1
	padding := width - ticksWidth - tview.TaggedStringWidth(lines[last])
	lines[last] += strings.Repeat(" ", max(1, padding)) + ticks
	return strings.Join(lines, "\n")
}

// messageWidth returns the width of the message panel
func messageWidth() int {
	if textView == nil {
		return 0
	}
	_, _, width, _ := textView.GetInnerRect()
	return width
}

// renderForWidth shows the messages again when the panel's width changed, which
// moves the ticks
func renderForWidth() {
	if width := messageWidth(); width != renderedWidth && !printedSinceRender.Load() {
		renderMessages()
	}
}
