package main

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

// drawText draws tagged text like the message panel does, and returns the screen
func drawText(t *testing.T, text string) tcell.SimulationScreen {
	screen := newScreen(t, 40, 6)
	view := tview.NewTextView().SetDynamicColors(true).SetRegions(true).SetWordWrap(true)
	view.SetText(text)
	view.SetRect(0, 0, 40, 6)
	view.Draw(screen)
	return screen
}

// newScreen returns a simulated screen of the size to draw on
func newScreen(t *testing.T, width, height int) tcell.SimulationScreen {
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(width, height)
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
	node := setNodeColor(tview.NewTreeNode("Alice"), tcell.ColorGreen)
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
			screen := newScreen(t, width, 30)
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
	screen := newScreen(t, 12, 8)
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
	screen := newScreen(t, 30, 4)
	root := setNodeColor(tview.NewTreeNode("Chats"), tcell.ColorNames[config.Config.Colors.ListHeader])
	root.AddChild(setNodeColor(tview.NewTreeNode(chatNodeText(messages.Chat{Id: "alice", Name: "Alice", Unread: 3})), tcell.ColorGreen))
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
	if colorOf('A') != tcell.ColorGray || colorOf('C') != tcell.ColorGray {
		t.Error("expected the chat names and header to be grey")
	}
}

func TestEscapeInEmptyInputScrollsToTheNewestMessages(t *testing.T) {
	textView = tview.NewTextView().SetDynamicColors(true).SetRegions(true)
	textView.SetRect(0, 0, 20, 3)
	for i := 0; i < 20; i++ {
		fmt.Fprintf(textView, "line %d\n", i)
	}
	textView.ScrollTo(0, 0)
	textInput = newTextInput()
	setInput("")
	messageSearch, chatSearch = "", ""

	EnterCommand(tcell.KeyEsc)
	screen := newScreen(t, 20, 3)
	textView.Draw(screen)
	if row, _ := textView.GetScrollOffset(); row == 0 {
		t.Fatal("expected Escape to scroll the chat down to the newest messages")
	}
}

func TestOpenWithCommandShowsItsOutput(t *testing.T) {
	textView = tview.NewTextView().SetDynamicColors(true)
	command := "echo"
	if runtime.GOOS == "windows" {
		command = "cmd /c echo"
	}
	if err := openWithCommand(command, "photo.jpg"); err != nil {
		t.Fatal(err)
	}
	// the command runs in the background
	for start := time.Now(); !strings.Contains(textView.GetText(true), "photo.jpg"); time.Sleep(10 * time.Millisecond) {
		if time.Since(start) > 5*time.Second {
			t.Fatalf("expected the output of the command with the file, got %q", textView.GetText(true))
		}
	}
}

func TestOpeningAChatScrollsToItsNewestMessages(t *testing.T) {
	sessionManager = &messages.SessionManager{CommandChannel: make(chan messages.Command, 10)}
	defer func() { sessionManager = nil }()
	textInput = newTextInput()
	chatRoot = tview.NewTreeNode("Chats")
	textView = tview.NewTextView().SetDynamicColors(true).SetRegions(true)
	textView.SetRect(0, 0, 20, 3)
	screen := newScreen(t, 20, 3)
	currentReceiver = messages.Chat{Id: "alice"}
	for i := 0; i < 20; i++ {
		fmt.Fprintf(textView, "line %d\n", i)
	}
	// scrolled up to read, with a message selected
	textView.ScrollTo(0, 0)
	textView.Highlight("1")

	SetDisplayedChat(messages.Chat{Id: "bob"})
	for i := 0; i < 20; i++ {
		fmt.Fprintf(textView, "line %d\n", i)
	}
	textView.Draw(screen)
	if row, _ := textView.GetScrollOffset(); row != 20-3+1 {
		t.Fatalf("expected the chat to show its newest messages, it is scrolled to row %d", row)
	}
}

func TestUnreadMessagesAndReactionsAreHighlighted(t *testing.T) {
	unread := tcell.ColorNames[config.Config.Colors.UnreadCount]
	currentReceiver = messages.Chat{Id: "alice", UnreadReactions: []int64{1300}}
	read := messages.Message{Id: "1", ContactId: "alice", ContactShort: "Alice", Timestamp: 1000, Text: "old"}
	mine := messages.Message{Id: "2", FromMe: true, Timestamp: 1100, Text: "mine",
		Reactions: map[string]string{"alice": "👍"}, ReactionTimes: map[string]int64{"alice": 1300}}
	fresh := messages.Message{Id: "3", ContactId: "alice", ContactShort: "Alice", Timestamp: 1200, Text: "new", Unread: true}
	screen := drawText(t, getTextMessageString(&read, nil)+"\n"+getTextMessageString(&fresh, &read))
	colorAt := func(x, y int) tcell.Color {
		fg, _, _ := styleAt(screen, x, y).Decompose()
		return fg
	}
	// the read message, then an empty line and the unread one with its own header
	if colorAt(0, 0) == unread {
		t.Error("expected the time of a read message not to be highlighted")
	}
	if r, _, _, _ := screen.GetContent(0, 3); r != '(' || colorAt(0, 3) != unread {
		t.Errorf("expected the unread message to have its own highlighted time, got %c", r)
	}

	screen = drawText(t, getTextMessageString(&mine, nil))
	found := false
	for x := 0; x < 10; x++ {
		if r, _, _, _ := screen.GetContent(x, 2); r == '↳' {
			found = true
			if colorAt(x, 2) != unread {
				t.Error("expected the arrow of a new reaction to be highlighted")
			}
		}
	}
	if !found {
		t.Fatal("expected the reactions below the message")
	}
	currentReceiver = messages.Chat{Id: "alice"}
	screen = drawText(t, getTextMessageString(&mine, nil))
	for x := 0; x < 10; x++ {
		if r, _, _, _ := screen.GetContent(x, 2); r == '↳' && colorAt(x, 2) == unread {
			t.Error("expected the arrow of seen reactions not to be highlighted")
		}
	}
}

func TestGreyedOutNoticesAreNotDimmer(t *testing.T) {
	screen := newScreen(t, 40, 4)
	treeView = tview.NewTreeView()
	textView = tview.NewTextView().SetDynamicColors(true)
	textView.SetRect(0, 0, 40, 4)
	textView.SetText("hi\n\n[::d]Your phone has no older messages[::-]")
	treeView.Focus(func(tview.Primitive) {}) // the chat panel is greyed out
	textView.Draw(screen)
	greyOutUnfocusedPanel(screen)
	fg, _, attr := styleAt(screen, 0, 2).Decompose()
	if fg != tcell.ColorGray || attr&tcell.AttrDim != 0 {
		t.Errorf("expected the notice to be grey like the rest, got %v with dim %v", fg, attr&tcell.AttrDim != 0)
	}
}
