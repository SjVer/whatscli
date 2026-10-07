package ui

import (
	"strings"
	"testing"

	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

func TestTicksAreAtTheRight(t *testing.T) {
	withUI(t)
	const width = 30
	view := tview.NewTextView().SetDynamicColors(true).SetRegions(true).SetWordWrap(true)
	view.SetRect(0, 0, width, 10)
	msg := messages.Message{Id: "1", FromMe: true, Timestamp: 1000, Status: messages.StatusRead,
		Text: "a longer message that has to wrap over a few lines"}
	view.SetText(getTextMessageString(&msg, nil, width))
	screen := newScreen(t, width, 10)
	view.Draw(screen)

	row := func(y int) string {
		text := ""
		for x := 0; x < width; x++ {
			r, _, _, _ := screen.GetContent(x, y)
			text += string(r)
		}
		return text
	}
	last := 0
	for y := 0; y < 10; y++ {
		if strings.TrimSpace(row(y)) != "" {
			last = y
		}
	}
	if !strings.HasSuffix(row(last), "✓✓") || strings.Contains(row(last-1), "✓") {
		t.Fatalf("expected the ticks at the right of the last line, got %q", row(last))
	}
	// the text leaves room for them on every line
	for y := 1; y < last; y++ {
		if r, _, _, _ := screen.GetContent(width-1, y); r != ' ' {
			t.Errorf("expected line %d to leave room for the ticks, got %q", y, row(y))
		}
	}
}

func TestTicksOfShortAndFormattedMessages(t *testing.T) {
	withUI(t)
	ticks := statusTicks(messages.StatusSent)
	if text := withTicks("", ticks, 30); tview.TaggedStringWidth(text) != 30 {
		t.Errorf("expected an empty text to get the ticks at the right, got %q", text)
	}
	if text := withTicks("[::b]bold[::-]", ticks, 30); !strings.HasPrefix(text, "[::b]bold[::-] ") || tview.TaggedStringWidth(text) != 30 {
		t.Errorf("expected formatting to be kept, got %q", text)
	}
	if text := withTicks("hi", ticks, 12); text != "hi" {
		t.Errorf("expected no ticks in a very narrow panel, got %q", text)
	}
}
