package main

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

// drawText draws tagged text like the message panel does, and returns the screen
func drawText(t *testing.T, text string) tcell.SimulationScreen {
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(40, 6)
	view := tview.NewTextView().SetDynamicColors(true).SetRegions(true).SetWordWrap(true)
	view.SetText(text)
	view.SetRect(0, 0, 40, 6)
	view.Draw(screen)
	return screen
}

func styleAt(screen tcell.SimulationScreen, x, y int) tcell.Style {
	_, _, style, _ := screen.GetContent(x, y)
	return style
}

func TestMessageFormattingIsDrawn(t *testing.T) {
	msg := messages.Message{Id: "1", FromMe: true, Timestamp: 1000, Text: "_it_ *bo*"}
	screen := drawText(t, getTextMessageString(&msg, nil))
	// the text is on the line below the header
	if _, _, attr := styleAt(screen, 0, 1).Decompose(); attr&tcell.AttrItalic == 0 {
		t.Error("expected _it_ to be drawn italic")
	}
	if _, _, attr := styleAt(screen, 3, 1).Decompose(); attr&tcell.AttrBold == 0 {
		t.Error("expected *bo* to be drawn bold")
	}
}

func TestHelpHeaderUnderlineEnds(t *testing.T) {
	screen := drawText(t, "[-::u]Keys:[-::U]\nGlobal")
	if styleAt(screen, 0, 0).GetUnderlineStyle() == tcell.UnderlineStyleNone {
		t.Error("expected the header to be underlined")
	}
	if styleAt(screen, 0, 1).GetUnderlineStyle() != tcell.UnderlineStyleNone {
		t.Error("expected the text after the header not to be underlined")
	}
}

func TestChatListEntriesUseConfiguredBackground(t *testing.T) {
	node := setNodeColor(tview.NewTreeNode("Mam"), tcell.ColorGreen)
	fg, bg, _ := node.GetTextStyle().Decompose()
	if fg != tcell.ColorGreen || bg != tcell.ColorNames[config.Config.Colors.Background] {
		t.Errorf("expected green on the configured background, got %v on %v", fg, bg)
	}
}
