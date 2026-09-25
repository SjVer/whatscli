package main

import (
	"strings"
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

func TestWrappedLineCountMatchesTheInput(t *testing.T) {
	texts := []string{
		"short",
		"one two three four five six seven eight nine ten",
		"a message with some longer words like international and communication",
		"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz",
		"line one\nline two is a bit longer than the others\n\nline four",
		"emoji 😭😭😭 in 😭 a 😭😭 line with words",
		"trailing spaces at the end of a line      then more",
	}
	for _, width := range []int{10, 17, 30} {
		for _, text := range texts {
			screen := tcell.NewSimulationScreen("")
			if err := screen.Init(); err != nil {
				t.Fatal(err)
			}
			screen.SetSize(width, 30)
			input := tview.NewTextArea()
			input.SetText(text, false)
			input.SetRect(0, 0, width, 30)
			input.Draw(screen)
			// the last row with text is how many lines it takes
			drawn := 0
			for y := 0; y < 30; y++ {
				for x := 0; x < width; x++ {
					if r, _, _, _ := screen.GetContent(x, y); r != ' ' && r != 0 {
						drawn = y + 1
					}
				}
			}
			// empty lines at the end aren't drawn, but count
			if strings.HasSuffix(text, "\n") {
				continue
			}
			if counted := wrappedLineCount(text, width); counted != drawn {
				t.Errorf("width %d, %q: counted %d lines, the input draws %d", width, text, counted, drawn)
			}
		}
	}
}

func TestGrownInputShowsAllLines(t *testing.T) {
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(12, 8)
	textInput = tview.NewTextArea()
	grid := tview.NewGrid().SetRows(1, 0, 1)
	grid.AddItem(textInput, 2, 0, 1, 1, 0, 0, true)
	grid.SetRect(0, 0, 12, 8)
	inputLines = 1

	// typing until the text wraps, while the input is one line high
	grid.Draw(screen)
	setInput("first line second")
	grid.Draw(screen)
	updateInputHeight(grid)
	grid.Draw(screen)

	row := func(y int) string {
		text := ""
		for x := 0; x < 12; x++ {
			r, _, _, _ := screen.GetContent(x, y)
			text += string(r)
		}
		return strings.TrimSpace(text)
	}
	if inputLines != 2 || row(6) != "first line" || row(7) != "second" {
		t.Fatalf("expected both lines at the bottom, got %d lines: %q, %q", inputLines, row(6), row(7))
	}
}

func TestGreyChatListKeepsUnreadCountsColored(t *testing.T) {
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(30, 4)
	root := setNodeColor(tview.NewTreeNode("Chats"), tcell.ColorNames[config.Config.Colors.ListHeader])
	root.AddChild(setNodeColor(tview.NewTreeNode(chatNodeText(messages.Chat{Id: "mam", Name: "Mam", Unread: 3})), tcell.ColorGreen))
	treeView = tview.NewTreeView().SetRoot(root).SetCurrentNode(root)
	treeView.SetRect(0, 0, 30, 4)
	textView = tview.NewTextView()
	textView.Focus(func(tview.Primitive) {}) // the chat list is greyed out
	treeView.Draw(screen)
	greyOutUnfocusedPanel(screen)

	colorOf := func(target rune) tcell.Color {
		for y := 0; y < 4; y++ {
			for x := 0; x < 30; x++ {
				if r, _, style, _ := screen.GetContent(x, y); r == target {
					fg, _, _ := style.Decompose()
					return fg
				}
			}
		}
		t.Fatalf("%c not drawn", target)
		return 0
	}
	if colorOf('3') != tcell.ColorNames[config.Config.Colors.UnreadCount] {
		t.Error("expected the unread count to keep its color")
	}
	if colorOf('M') != tcell.ColorGray || colorOf('C') != tcell.ColorGray {
		t.Error("expected the chat names and header to be grey")
	}
}
