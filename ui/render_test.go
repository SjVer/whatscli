package ui

import (
	"fmt"
	"strings"
	"testing"

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
	withUI(t)
	msg := messages.Message{Id: "1", ContactShort: "Bob", Timestamp: 1000, Text: "_it_ *bo*"}
	screen := drawText(t, getTextMessageString(&msg, nil, 0))
	// the text is on the line below the header
	if _, _, attr := styleAt(screen, 0, 1).Decompose(); attr&tcell.AttrItalic == 0 {
		t.Error("expected _it_ to be drawn italic")
	}
	if _, _, attr := styleAt(screen, 3, 1).Decompose(); attr&tcell.AttrBold == 0 {
		t.Error("expected *bo* to be drawn bold")
	}
}

func TestHelpHeaderUnderlineEnds(t *testing.T) {
	withUI(t)
	screen := drawText(t, "[-::u]Keys:[-::U]\nGlobal")
	if styleAt(screen, 0, 0).GetUnderlineStyle() == tcell.UnderlineStyleNone {
		t.Error("expected the header to be underlined")
	}
	if styleAt(screen, 0, 1).GetUnderlineStyle() != tcell.UnderlineStyleNone {
		t.Error("expected the text after the header not to be underlined")
	}
}

func TestChatListEntriesUseConfiguredBackground(t *testing.T) {
	withUI(t)
	node := setNodeColor(tview.NewTreeNode("Alice"), tcell.ColorGreen)
	fg, bg, _ := node.GetTextStyle().Decompose()
	if fg != tcell.ColorGreen || bg != tcell.ColorNames[config.Config.Colors.Background] {
		t.Errorf("expected green on the configured background, got %v on %v", fg, bg)
	}
}

func TestWrappedLineCountMatchesTheInput(t *testing.T) {
	withUI(t)
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
	withUI(t)
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
	withUI(t)
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
	withUI(t)
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

func TestOpeningAChatScrollsToItsNewestMessages(t *testing.T) {
	withUI(t)
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
	withUI(t)
	unread := tcell.ColorNames[config.Config.Colors.UnreadCount]
	currentReceiver = messages.Chat{Id: "alice", UnreadReactions: []int64{1300}}
	read := messages.Message{Id: "1", ContactId: "alice", ContactShort: "Alice", Timestamp: 1000, Text: "old"}
	mine := messages.Message{Id: "2", FromMe: true, Timestamp: 1100, Text: "mine",
		Reactions: map[string]string{"alice": "👍"}, ReactionTimes: map[string]int64{"alice": 1300}}
	fresh := messages.Message{Id: "3", ContactId: "alice", ContactShort: "Alice", Timestamp: 1200, Text: "new", Unread: true}
	screen := drawText(t, getTextMessageString(&read, nil, 0)+"\n"+getTextMessageString(&fresh, &read, 0))
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

	screen = drawText(t, getTextMessageString(&mine, nil, 0))
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
	screen = drawText(t, getTextMessageString(&mine, nil, 0))
	for x := 0; x < 10; x++ {
		if r, _, _, _ := screen.GetContent(x, 2); r == '↳' && colorAt(x, 2) == unread {
			t.Error("expected the arrow of seen reactions not to be highlighted")
		}
	}
}

func TestGreyedOutNoticesAreNotDimmer(t *testing.T) {
	withUI(t)
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

func TestProfileNamesAreItalic(t *testing.T) {
	withUI(t)
	defer func(check func(string) bool) { isProfileName = check }(isProfileName)
	isProfileName = func(id string) bool { return id == "bob" }
	// the attributes of the first letter of a name in the header
	nameStyle := func(msg messages.Message, initial rune) tcell.AttrMask {
		screen := drawText(t, getTextMessageString(&msg, nil, 0))
		for x := 0; x < 40; x++ {
			if r, _, _, _ := screen.GetContent(x, 0); r == initial {
				_, _, attr := styleAt(screen, x, 0).Decompose()
				return attr
			}
		}
		t.Fatalf("%c not drawn", initial)
		return 0
	}
	if nameStyle(messages.Message{Id: "1", ContactId: "bob", ContactShort: "Bob", Text: "hi"}, 'B')&tcell.AttrItalic == 0 {
		t.Error("expected the profile name to be italic")
	}
	if nameStyle(messages.Message{Id: "2", ContactId: "alice", ContactShort: "Alice", Text: "hi"}, 'A')&tcell.AttrItalic != 0 {
		t.Error("expected a saved name not to be italic")
	}
}

func TestShowingAnAttachmentKeepsThePlace(t *testing.T) {
	withUI(t)
	defer func(view *tview.TextView) { sessionManager, app, textView = nil, nil, view }(textView)
	sessionManager = &messages.SessionManager{CommandChannel: make(chan messages.Command, 10)}
	app = tview.NewApplication()
	textView = tview.NewTextView().SetDynamicColors(true).SetRegions(true)
	textView.SetRect(0, 0, 20, 3)
	for i := 0; i < 20; i++ {
		fmt.Fprintf(textView, "[\"m%d\"]line %d[\"\"]\n", i, i)
	}
	textView.Highlight("m2")
	textView.ScrollTo(2, 0)

	handleMessageCommand("open")(nil)
	if command := <-sessionManager.CommandChannel; command.Name != "open" || command.Params[0] != "m2" {
		t.Fatalf("expected the selected message to be shown, got %+v", command)
	}
	textView.Draw(newScreen(t, 20, 3))
	if row, _ := textView.GetScrollOffset(); row != 2 || len(textView.GetHighlights()) == 0 {
		t.Errorf("expected the place and the selection to stay, got row %d and %v", row, textView.GetHighlights())
	}
}

func TestScrollingByTheConfiguredLines(t *testing.T) {
	withUI(t)
	defer func(view *tview.TextView, lines int) { textView, config.Config.Ui.ScrollLines = view, lines }(textView, config.Config.Ui.ScrollLines)
	config.Config.Ui.ScrollLines = 3
	textView = tview.NewTextView()
	textView.SetRect(0, 0, 20, 5)
	for i := 0; i < 30; i++ {
		fmt.Fprintf(textView, "line %d\n", i)
	}
	screen := newScreen(t, 20, 5)
	textView.ScrollTo(10, 0)
	scrollWithWheel(tview.MouseScrollUp, nil)
	textView.Draw(screen)
	if row, _ := textView.GetScrollOffset(); row != 7 {
		t.Errorf("expected the wheel to scroll 3 lines up, got row %d", row)
	}
	// scrolling down to the end follows new messages again
	for i := 0; i < 10; i++ {
		scrollWithWheel(tview.MouseScrollDown, nil)
	}
	fmt.Fprintln(textView, "newest")
	textView.Draw(screen)
	last, _, _, _ := screen.GetContent(0, 4)
	beforeLast, _, _, _ := screen.GetContent(0, 3)
	if last != 'n' && beforeLast != 'n' {
		t.Errorf("expected the newest line at the bottom after scrolling to the end")
	}
}

func TestShowingTheChatListAgainKeepsTheOpenChat(t *testing.T) {
	withUI(t)
	defer func(view *tview.TextView, tree *tview.TreeView, root *tview.TreeNode, chat messages.Chat, chats []messages.Chat) {
		sessionManager, textView, treeView, chatRoot, currentReceiver, allChats = nil, view, tree, root, chat, chats
	}(textView, treeView, chatRoot, currentReceiver, allChats)
	sessionManager = &messages.SessionManager{CommandChannel: make(chan messages.Command, 10)}
	textView = tview.NewTextView()
	MakeTree()
	treeView.SetRect(0, 0, 30, 10)
	alice := messages.Chat{Id: "alice", Name: "Alice"}
	allChats = []messages.Chat{alice, {Id: "bob", Name: "Bob"}}
	currentReceiver = alice
	renderChats()
	treeView.Draw(newScreen(t, 30, 10))
	fmt.Fprint(textView, "info of a message")

	// e.g. when a message arrived
	allChats[0].Unread = 1
	renderChats()
	treeView.Draw(newScreen(t, 30, 10))
	if len(sessionManager.CommandChannel) != 0 || textView.GetText(true) != "info of a message" {
		t.Errorf("expected the open chat to stay as it is, got %d commands and %q", len(sessionManager.CommandChannel), textView.GetText(true))
	}
	if currentReceiver.Unread != 1 {
		t.Error("expected the open chat to be the one of the new list")
	}
}

func TestArchiveKeyOfTheChatList(t *testing.T) {
	withUI(t)
	defer func(tree *tview.TreeView) { sessionManager, treeView = nil, tree }(treeView)
	sessionManager = &messages.SessionManager{CommandChannel: make(chan messages.Command, 10)}
	for _, test := range []struct {
		chat    messages.Chat
		command string
	}{
		{messages.Chat{Id: "alice"}, "archive"},
		{messages.Chat{Id: "bob", InArchive: true}, "unarchive"},
	} {
		treeView = tview.NewTreeView().SetCurrentNode(tview.NewTreeNode("").SetReference(test.chat))
		handleChatArchive(nil)
		if command := <-sessionManager.CommandChannel; command.Name != test.command || command.Params[0] != test.chat.Id {
			t.Errorf("expected %s of %s, got %+v", test.command, test.chat.Id, command)
		}
	}
	// nothing for the archived chats folder
	treeView = tview.NewTreeView().SetCurrentNode(tview.NewTreeNode("Archived").SetReference("archived"))
	handleChatArchive(nil)
	if len(sessionManager.CommandChannel) != 0 {
		t.Error("expected the folder not to be archived")
	}
}

func TestAMessageThatArrivesLooksLikeTheOthers(t *testing.T) {
	withUI(t)
	defer func(view *tview.TextView, chat messages.Chat) { textView, currentReceiver = view, chat }(textView, currentReceiver)
	notices, messageSearch = map[string][]notice{}, ""
	currentReceiver = messages.Chat{Id: "alice"}
	shown := []messages.Message{
		{Id: "1", ChatId: "alice", ContactId: "alice", ContactShort: "Alice", Timestamp: 1000, Text: "hi",
			Reactions: map[string]string{messages.ReactorMe: "👍"}, ReactionTimes: map[string]int64{messages.ReactorMe: 1050}},
		{Id: "2", ChatId: "alice", FromMe: true, Timestamp: 1100, Text: "hello there", Status: messages.StatusRead},
	}
	screenOf := func() string {
		screen := newScreen(t, 40, 20)
		textView.Draw(screen)
		out := ""
		for y := 0; y < 20; y++ {
			for x := 0; x < 40; x++ {
				r, _, _, _ := screen.GetContent(x, y)
				out += string(r)
			}
			out += "\n"
		}
		return out
	}
	for _, arrived := range []messages.Message{
		{Id: "3", ChatId: "alice", ContactId: "alice", ContactShort: "Alice", Timestamp: 1200, Text: "how are you", Unread: true},
		{Id: "4", ChatId: "alice", FromMe: true, Timestamp: 1150, Text: "more", Status: messages.StatusSent},
	} {
		textView = tview.NewTextView().SetDynamicColors(true).SetRegions(true).SetWordWrap(true)
		textView.SetRect(0, 0, 40, 20)
		chatMessages = append([]messages.Message(nil), shown...)
		renderMessages()
		// drawn before it arrives, which tview writes to differently
		textView.Draw(newScreen(t, 40, 20))
		showNewMessage(arrived)
		added := screenOf()
		chatMessages = append(append([]messages.Message(nil), shown...), arrived)
		renderMessages()
		if full := screenOf(); added != full {
			t.Errorf("expected message %s to look as when the chat is shown again:\n%s\ngot:\n%s", arrived.Id, full, added)
		}
	}
}

func TestMediaLabelsHaveTheirOwnColor(t *testing.T) {
	withUI(t)
	row := func(screen tcell.SimulationScreen, y int) string {
		text := ""
		for x := 0; x < 40; x++ {
			r, _, _, _ := screen.GetContent(x, y)
			text += string(r)
		}
		return strings.TrimSpace(text)
	}
	msg := messages.Message{Id: "1", ContactShort: "Bob", Timestamp: 1000, Kind: messages.MessageKindImage,
		Text: "[IMAGE] look", AltText: "a dog"}
	screen := drawText(t, getTextMessageString(&msg, nil, 0))
	color := tcell.ColorNames[config.Config.Colors.MediaLabel]
	if fg, _, _ := styleAt(screen, 0, 1).Decompose(); fg != color {
		t.Errorf("expected the label in its color, got %v", fg)
	}
	// the caption on its own line, shown like other text
	if row(screen, 1) != "[IMAGE: a dog]" || row(screen, 2) != "look" {
		t.Errorf("expected the caption below the label, got %q and %q", row(screen, 1), row(screen, 2))
	}
	if fg, _, _ := styleAt(screen, 0, 2).Decompose(); fg == color {
		t.Error("expected the caption not to be in the label color")
	}
	// the name of a document stays next to its label
	document := messages.Message{Id: "2", ContactShort: "Bob", Timestamp: 1000, Kind: messages.MessageKindDocument, Text: "[DOCUMENT] notes.pdf"}
	if screen = drawText(t, getTextMessageString(&document, nil, 0)); row(screen, 1) != "[DOCUMENT] notes.pdf" {
		t.Errorf("expected the document name after the label, got %q", row(screen, 1))
	}
}

func TestStatusMessagesAreDim(t *testing.T) {
	withUI(t)
	defer func(view *tview.TextView) { textView = view }(textView)
	textView = tview.NewTextView().SetDynamicColors(true)
	textView.SetRect(0, 0, 40, 3)
	UiHandler{}.PrintText("No unread messages in current chat")
	PrintHint("there is no image on the clipboard")
	screen := newScreen(t, 40, 3)
	textView.Draw(screen)
	for y := 0; y < 2; y++ {
		if _, _, attr := styleAt(screen, 0, y).Decompose(); attr&tcell.AttrDim == 0 {
			t.Errorf("expected line %d to be dim", y)
		}
	}
}

// withUI restores the globals of the UI when the test ends, which it can change
func withUI(t *testing.T) {
	view, input, tree, root, application, manager := textView, textInput, treeView, chatRoot, app, sessionManager
	chat, msgs, regions, chats := currentReceiver, chatMessages, curRegions, allChats
	savedNotices, savedDrafts, emoji := notices, drafts, recentEmoji
	search, listSearch, reply, image, react, lines := messageSearch, chatSearch, replyTarget, pastedImage, reactTarget, inputLines
	reaction, printed := endedWithReaction, printedSinceRender.Load()
	t.Cleanup(func() {
		textView, textInput, treeView, chatRoot, app, sessionManager = view, input, tree, root, application, manager
		currentReceiver, chatMessages, curRegions, allChats = chat, msgs, regions, chats
		notices, drafts, recentEmoji = savedNotices, savedDrafts, emoji
		messageSearch, chatSearch, replyTarget, pastedImage, reactTarget, inputLines = search, listSearch, reply, image, react, lines
		endedWithReaction = reaction
		printedSinceRender.Store(printed)
	})
}
