package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"codeberg.org/tslocum/cbind"
	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
	"github.com/skratchdot/open-golang/open"
	"github.com/zyedidia/clipboard"
)

var VERSION string = "v1.1.6"

var sndTxt string = ""
var currentReceiver messages.Chat = messages.Chat{}
var curRegions []messages.Message

var textView *tview.TextView
var treeView *tview.TreeView
var textInput *tview.TextArea
var topBar *tview.TextView
var infoBar *tview.TextView

var chatRoot *tview.TreeNode
var archivedExpanded bool

// all chats as last set by the session manager, and the text the chat list is filtered by
var allChats []messages.Chat
var chatSearch string

// the message that /react reacts to, selected with the react key
var reactTarget string

// all messages of the displayed chat, and the text they are filtered by
var chatMessages []messages.Message
var messageSearch string
var app *tview.Application

var sessionManager *messages.SessionManager

var keyBindings *cbind.Configuration

var uiHandler messages.UiMessageHandler

func main() {
	dump := flag.Bool("dump", false, "print the chat list and unread messages instead of starting the UI")
	dumpWait := flag.Duration("dump-wait", 15*time.Second, "how long -dump waits for messages received while offline")
	dumpChat := flag.String("dump-chat", "", "with -dump, load this chat from the phone and print its messages")
	logPath := flag.String("log", "", "write the WhatsApp connection log to this file")
	flag.Parse()

	config.InitConfig()
	logger, err := openLog(*logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if *dump {
		os.Exit(runDump(*dumpWait, *dumpChat, logger))
	}
	uiHandler = UiHandler{}
	sessionManager = &messages.SessionManager{Log: logger}
	sessionManager.Init(uiHandler)

	app = tview.NewApplication()

	sideBarWidth := config.Config.Ui.ChatSidebarWidth
	gridLayout := tview.NewGrid()
	gridLayout.SetRows(1, 0, 1)
	gridLayout.SetColumns(sideBarWidth, 0, sideBarWidth)
	gridLayout.SetBorders(true)
	gridLayout.SetBackgroundColor(tcell.ColorNames[config.Config.Colors.Background])
	gridLayout.SetBordersColor(tcell.ColorNames[config.Config.Colors.Borders])

	cmdPrefix := config.Config.General.CmdPrefix
	topBar = tview.NewTextView()
	topBar.SetDynamicColors(true)
	topBar.SetScrollable(false)
	topBar.SetText("[::b] WhatsCLI " + VERSION + "  [-::d]Type " + cmdPrefix + "help or press " + config.Config.Keymap.CommandHelp + " for help")
	topBar.SetBackgroundColor(tcell.ColorNames[config.Config.Colors.Background])

	infoBar = tview.NewTextView()
	infoBar.SetDynamicColors(true)
	infoBar.SetBackgroundColor(tcell.ColorNames[config.Config.Colors.Background])
	UpdateStatusBar(messages.SessionStatus{})

	textView = tview.NewTextView().
		SetDynamicColors(true).
		SetRegions(true).
		SetWordWrap(true).
		SetChangedFunc(func() {
			app.Draw()
		})
	textView.SetBackgroundColor(tcell.ColorNames[config.Config.Colors.Background])
	textView.SetTextColor(tcell.ColorNames[config.Config.Colors.Text])

	PrintHelp()

	textInput = newTextInput()
	loadRecentEmoji()

	gridLayout.AddItem(topBar, 0, 0, 1, 4, 0, 0, false)
	gridLayout.AddItem(infoBar, 2, 0, 1, 1, 0, 0, false)
	gridLayout.AddItem(MakeTree(), 1, 0, 1, 1, 0, 0, false)
	gridLayout.AddItem(textView, 1, 1, 1, 3, 0, 0, false)
	gridLayout.AddItem(textInput, 2, 1, 1, 3, 0, 0, false)

	app.SetRoot(gridLayout, true)
	app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		updateInputHeight(gridLayout)
		return false
	})
	app.SetAfterDrawFunc(func(screen tcell.Screen) {
		greyOutUnfocusedPanel(screen)
		drawEmojiPopup(screen)
	})
	app.EnableMouse(true)
	// pasted text arrives in one piece, so line breaks don't send it line by line
	app.EnablePaste(true)
	app.SetFocus(textInput)
	if err := sessionManager.StartManager(); err != nil {
		PrintError(err)
	}
	LoadShortcuts()
	// keeps how long ago the last sync was up to date
	go func() {
		for range time.Tick(time.Second) {
			app.QueueUpdateDraw(func() {
				UpdateStatusBar(lastStatus)
			})
		}
	}()
	app.Run()
	// saves changes of the last second too
	sessionManager.Close()
}

// newTextInput creates the input for messages and commands
func newTextInput() *tview.TextArea {
	input := tview.NewTextArea()
	input.SetBackgroundColor(tcell.ColorNames[config.Config.Colors.Background])
	input.SetTextStyle(tcell.StyleDefault.
		Background(tcell.ColorNames[config.Config.Colors.InputBackground]).
		Foreground(tcell.ColorNames[config.Config.Colors.InputText]))
	input.SetChangedFunc(func() {
		sndTxt = textInput.GetText()
		updateEmojiSuggestions()
	})
	input.SetMovedFunc(updateEmojiSuggestions)
	input.SetInputCapture(handleInputKeys)
	emojiPopup.dismissed = -1
	return input
}

// handles keys of the input field before it does
func handleInputKeys(event *tcell.EventKey) *tcell.EventKey {
	if handleEmojiPopupKeys(event) {
		return nil
	}
	// word-wise moving and deleting. Ctrl+Left and selecting with Ctrl+Shift
	// are handled by the input field.
	if event.Modifiers()&tcell.ModCtrl != 0 {
		switch event.Key() {
		case tcell.KeyRight:
			// the input field stops before the last letter of a word instead
			if event.Modifiers()&tcell.ModShift == 0 {
				_, _, end := textInput.GetSelection()
				next := nextWordStart(textInput.GetText(), end)
				textInput.Select(next, next)
				return nil
			}
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			return tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModCtrl)
		case tcell.KeyDelete:
			// delete until the start of the next word, or the selection
			if selected, cursor, _ := textInput.GetSelection(); selected == "" {
				textInput.Replace(cursor, nextWordStart(textInput.GetText(), cursor), "")
				return nil
			}
			return tcell.NewEventKey(tcell.KeyDelete, 0, tcell.ModNone)
		}
	}
	switch event.Key() {
	case tcell.KeyEnter:
		// Enter sends, Shift+Enter or Alt+Enter starts a new line
		if event.Modifiers()&(tcell.ModShift|tcell.ModAlt) != 0 {
			return tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)
		}
		EnterCommand(tcell.KeyEnter)
		return nil
	case tcell.KeyEscape:
		EnterCommand(tcell.KeyEscape)
		return nil
	case tcell.KeyUp, tcell.KeyDown:
		// move between the lines of a longer message, else scroll the messages
		if inputLines > 1 {
			return event
		}
	}
	if event.Key() == tcell.KeyDown {
		offset, _ := textView.GetScrollOffset()
		offset += 1
		textView.ScrollTo(offset, 0)
		return nil
	}
	if event.Key() == tcell.KeyUp {
		offset, _ := textView.GetScrollOffset()
		offset -= 1
		textView.ScrollTo(offset, 0)
		return nil
	}
	if event.Key() == tcell.KeyPgDn {
		offset, _ := textView.GetScrollOffset()
		offset += 10
		textView.ScrollTo(offset, 0)
		return nil
	}
	if event.Key() == tcell.KeyPgUp {
		offset, _ := textView.GetScrollOffset()
		offset -= 10
		textView.ScrollTo(offset, 0)
		return nil
	}
	return event
}

// maxInputLines is how high the input grows for longer messages
const maxInputLines = 8

// inputLines is the current height of the input
var inputLines = 1

// setInput sets the text of the input, with the cursor at the end
func setInput(text string) {
	textInput.SetText(text, true)
	sndTxt = text
	updateEmojiSuggestions()
}

// updateInputHeight grows the input to fit its text, up to maxInputLines
func updateInputHeight(grid *tview.Grid) {
	_, _, width, _ := textInput.GetInnerRect()
	needed := wrappedLineCount(textInput.GetText(), width)
	lines := min(needed, maxInputLines)
	if lines != inputLines {
		inputLines = lines
		grid.SetRows(1, 0, lines)
	}
	// Before it grew, the input scrolled down to the cursor on the new line.
	// Scroll back up when all lines fit, so that none of them is hidden.
	if row, column := textInput.GetOffset(); needed <= maxInputLines && row != 0 {
		textInput.SetOffset(0, column)
	}
}

// nextWordStart returns where the word after the one at pos starts in text,
// skipping the rest of the word at pos and the spaces after it
func nextWordStart(text string, pos int) int {
	for pos < len(text) {
		r, size := utf8.DecodeRuneInString(text[pos:])
		if unicode.IsSpace(r) {
			break
		}
		pos += size
	}
	for pos < len(text) {
		r, size := utf8.DecodeRuneInString(text[pos:])
		if !unicode.IsSpace(r) {
			break
		}
		pos += size
	}
	return pos
}

// wordPattern matches a word with the spaces after it
var wordPattern = regexp.MustCompile(`\S*\s*`)

// wrappedLineCount returns on how many lines text is shown when wrapped at
// words to width, like the input does
func wrappedLineCount(text string, width int) int {
	if width <= 0 {
		return 1
	}
	lines := 0
	for _, paragraph := range strings.Split(text, "\n") {
		lines++
		lineWidth := 0
		for _, word := range wordPattern.FindAllString(paragraph, -1) {
			trimmed := strings.TrimRight(word, " ")
			wordWidth := uniseg.StringWidth(trimmed)
			if lineWidth > 0 && lineWidth+wordWidth > width {
				lines++
				lineWidth = 0
			}
			// words longer than a line are broken up
			for wordWidth > width {
				lines++
				wordWidth -= width
			}
			// spaces don't go past the end of a line, but continue on the next
			lineWidth += wordWidth + len(word) - len(trimmed)
			for lineWidth > width {
				lines++
				lineWidth -= width
			}
		}
	}
	return lines
}

// colors an entry of the chat list, on the configured background
func setNodeColor(node *tview.TreeNode, color tcell.Color) *tview.TreeNode {
	node.SetColor(color)
	return node.SetTextStyle(node.GetTextStyle().Background(tcell.ColorNames[config.Config.Colors.Background]))
}

// creates the TreeView for chats
func MakeTree() *tview.TreeView {
	rootDir := "Chats"
	chatRoot = setNodeColor(tview.NewTreeNode(rootDir), tcell.ColorNames[config.Config.Colors.ListHeader])
	treeView = tview.NewTreeView().
		SetRoot(chatRoot).
		SetCurrentNode(chatRoot)
	treeView.SetBackgroundColor(tcell.ColorNames[config.Config.Colors.Background])

	// If a chat was selected, open it.
	treeView.SetChangedFunc(func(node *tview.TreeNode) {
		// the root and the archived chats folder show no chat, so that
		// collapsing the folder isn't undone for the open archived chat
		recv, _ := node.GetReference().(messages.Chat)
		SetDisplayedChat(recv)
	})
	// Collapse or expand the archived chats folder when it is selected.
	treeView.SetSelectedFunc(func(node *tview.TreeNode) {
		if len(node.GetChildren()) > 0 && node != chatRoot {
			archivedExpanded = !node.IsExpanded()
			node.SetExpanded(archivedExpanded)
		}
	})
	return treeView
}

// turns all text grey in the chat panel while the chat list has focus, and in
// the chat list otherwise
func greyOutUnfocusedPanel(screen tcell.Screen) {
	var unfocused *tview.Box
	if treeView.HasFocus() {
		unfocused = textView.Box
	} else {
		unfocused = treeView.Box
	}
	x, y, width, height := unfocused.GetInnerRect()
	for cy := y; cy < y+height; cy++ {
		for cx := x; cx < x+width; cx++ {
			mainc, combc, style, _ := screen.GetContent(cx, cy)
			// unread counts keep their color, to still stand out
			if !isUnreadCount(style) {
				screen.SetContent(cx, cy, mainc, combc, style.Foreground(tcell.ColorGray))
			}
		}
	}
}

// isUnreadCount returns whether a cell of the chat list shows an unread count
func isUnreadCount(style tcell.Style) bool {
	fg, _, attr := style.Decompose()
	return fg == tcell.ColorNames[config.Config.Colors.UnreadCount] && attr&tcell.AttrBold != 0
}

func handleFocusMessage(ev *tcell.EventKey) *tcell.EventKey {
	if !textView.HasFocus() {
		app.SetFocus(textView)
		if curRegions != nil && len(curRegions) > 0 {
			textView.Highlight(curRegions[len(curRegions)-1].Id)
		}
	}
	return nil
}

func handleFocusInput(ev *tcell.EventKey) *tcell.EventKey {
	ResetMsgSelection()
	if !textInput.HasFocus() {
		app.SetFocus(textInput)
	}
	return nil
}

func handleFocusContacts(ev *tcell.EventKey) *tcell.EventKey {
	ResetMsgSelection()
	if !treeView.HasFocus() {
		app.SetFocus(treeView)
	}
	return nil
}

func handleSwitchPanels(ev *tcell.EventKey) *tcell.EventKey {
	// Tab cycles the emoji suggestions while they are shown
	if textInput.HasFocus() && handleEmojiPopupKeys(ev) {
		return nil
	}
	ResetMsgSelection()
	if !textInput.HasFocus() {
		app.SetFocus(textInput)
	} else {
		app.SetFocus(treeView)
	}
	return nil
}

func handleCommand(command string) func(ev *tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		sessionManager.CommandChannel <- messages.Command{command, nil}
		return nil
	}
}

func handleCopyUser(ev *tcell.EventKey) *tcell.EventKey {
	if hls := textView.GetHighlights(); len(hls) > 0 {
		for _, val := range curRegions {
			if val.Id == hls[0] {
				clipboard.WriteAll(val.ContactId, "clipboard")
				PrintText("copied id of " + val.ContactName + " to clipboard")
			}
		}
		ResetMsgSelection()
	} else if currentReceiver.Id != "" {
		clipboard.WriteAll(currentReceiver.Id, "clipboard")
		PrintText("copied id of " + currentReceiver.Name + " to clipboard")
	}
	return nil
}

func handlePasteUser(ev *tcell.EventKey) *tcell.EventKey {
	if clip, err := safeReadClipboard(); err == nil {
		setInput(textInput.GetText() + " " + clip)
	} else {
		PrintError(err)
	}
	return nil
}

func safeReadClipboard() (clip string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("clipboard paste is unavailable: %v", rec)
		}
	}()
	return clipboard.ReadAll("clipboard")
}

func handleQuit(ev *tcell.EventKey) *tcell.EventKey {
	sessionManager.CommandChannel <- messages.Command{"disconnect", nil}
	app.Stop()
	return nil
}

func handleHelp(ev *tcell.EventKey) *tcell.EventKey {
	PrintHelp()
	return nil
}

func handleMessageCommand(command string) func(ev *tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		hls := textView.GetHighlights()
		if len(hls) > 0 {
			sessionManager.CommandChannel <- messages.Command{command, []string{hls[0]}}
			ResetMsgSelection()
			app.SetFocus(textInput)
		}
		return nil
	}
}

// starts a reaction to the selected message, the emoji is typed in the input
func handleMessageReact(ev *tcell.EventKey) *tcell.EventKey {
	hls := textView.GetHighlights()
	if len(hls) == 0 {
		return nil
	}
	reactTarget = hls[0]
	setInput(config.Config.General.CmdPrefix + "react ")
	app.SetFocus(textInput)
	return nil
}

// React reacts with an emoji to the message selected with the react key, or
// removes the reaction without one
func React(emoji string) {
	target := reactTarget
	reactTarget = ""
	if target == "" {
		if hls := textView.GetHighlights(); len(hls) > 0 {
			target = hls[0]
		}
	}
	if target == "" {
		PrintText("select a message first: " + config.Config.Keymap.FocusMessages + " and up/down, then " + config.Config.Keymap.MessageReact)
		return
	}
	sessionManager.CommandChannel <- messages.Command{Name: "react", Params: []string{target, replaceShortcodes(emoji)}}
	ResetMsgSelection()
}

func handleMessagesMove(amount int) func(ev *tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		if curRegions == nil || len(curRegions) == 0 {
			return nil
		}
		hls := textView.GetHighlights()
		if len(hls) > 0 {
			newId := GetOffsetMsgId(hls[0], amount)
			if newId != "" {
				textView.Highlight(newId)
			}
		} else {
			if amount < 0 {
				textView.Highlight(curRegions[0].Id)
			} else {
				textView.Highlight(curRegions[len(curRegions)-1].Id)
			}
		}
		textView.ScrollToHighlight()
		return nil
	}
}

// clears the chat search, or else goes back to Chats, the root of the chat
// list, and closes the archived chats
func handleExitChats(ev *tcell.EventKey) *tcell.EventKey {
	if chatSearch != "" {
		chatSearch = ""
		renderChats()
		return nil
	}
	archivedExpanded = false
	for _, node := range chatRoot.GetChildren() {
		if node.GetReference() == "archived" {
			node.SetExpanded(false)
		}
	}
	if treeView.GetCurrentNode() != chatRoot {
		treeView.SetCurrentNode(chatRoot)
		SetDisplayedChat(messages.Chat{})
	}
	return nil
}

func handleChatPanelUp(ev *tcell.EventKey) *tcell.EventKey {
	//TODO: scroll selection in treeView? or chatRoot? How?
	return ev
}

func handleChatPanelDown(ev *tcell.EventKey) *tcell.EventKey {
	return ev
}

func handleMessagesLast(ev *tcell.EventKey) *tcell.EventKey {
	if curRegions == nil || len(curRegions) == 0 {
		return nil
	}
	textView.Highlight(curRegions[len(curRegions)-1].Id)
	textView.ScrollToHighlight()
	return nil
}

func handleMessagesFirst(ev *tcell.EventKey) *tcell.EventKey {
	if curRegions == nil || len(curRegions) == 0 {
		return nil
	}
	textView.Highlight(curRegions[0].Id)
	textView.ScrollToHighlight()
	return nil
}

func handleExitMessages(ev *tcell.EventKey) *tcell.EventKey {
	if messageSearch != "" {
		Search("")
		return nil
	}
	if curRegions == nil || len(curRegions) == 0 {
		return nil
	}
	ResetMsgSelection()
	app.SetFocus(textInput)
	return nil
}

// load the key map
func LoadShortcuts() {
	// global bindings for app
	keyBindings = cbind.NewConfiguration()
	if err := keyBindings.Set(config.Config.Keymap.FocusMessages, handleFocusMessage); err != nil {
		PrintErrorMsg("focus_messages:", err)
	}
	if err := keyBindings.Set(config.Config.Keymap.FocusInput, handleFocusInput); err != nil {
		PrintErrorMsg("focus_input:", err)
	}
	if err := keyBindings.Set(config.Config.Keymap.FocusChats, handleFocusContacts); err != nil {
		PrintErrorMsg("focus_contacts:", err)
	}
	if err := keyBindings.Set(config.Config.Keymap.SwitchPanels, handleSwitchPanels); err != nil {
		PrintErrorMsg("switch_panels:", err)
	}
	if err := keyBindings.Set(config.Config.Keymap.CommandRead, handleCommand("read")); err != nil {
		PrintErrorMsg("command_read:", err)
	}
	if err := keyBindings.Set(config.Config.Keymap.Copyuser, handleCopyUser); err != nil {
		PrintErrorMsg("copyuser:", err)
	}
	if err := keyBindings.Set(config.Config.Keymap.Pasteuser, handlePasteUser); err != nil {
		PrintErrorMsg("pasteuser:", err)
	}
	if err := keyBindings.Set(config.Config.Keymap.CommandBacklog, handleCommand("backlog")); err != nil {
		PrintErrorMsg("command_backlog:", err)
	}
	if err := keyBindings.Set(config.Config.Keymap.CommandConnect, handleCommand("login")); err != nil {
		PrintErrorMsg("command_connect:", err)
	}
	if err := keyBindings.Set(config.Config.Keymap.CommandQuit, handleQuit); err != nil {
		PrintErrorMsg("command_quit:", err)
	}
	if err := keyBindings.Set(config.Config.Keymap.CommandHelp, handleHelp); err != nil {
		PrintErrorMsg("command_help:", err)
	}
	app.SetInputCapture(keyBindings.Capture)
	// bindings for chat message text view
	keysMessages := cbind.NewConfiguration()
	if err := keysMessages.Set(config.Config.Keymap.MessageDownload, handleMessageCommand("download")); err != nil {
		PrintErrorMsg("message_download:", err)
	}
	if err := keysMessages.Set(config.Config.Keymap.MessageOpen, handleMessageCommand("open")); err != nil {
		PrintErrorMsg("message_open:", err)
	}
	if err := keysMessages.Set(config.Config.Keymap.Copyuser, handleCopyUser); err != nil {
		PrintErrorMsg("copyuser:", err)
	}
	if err := keysMessages.Set(config.Config.Keymap.Pasteuser, handlePasteUser); err != nil {
		PrintErrorMsg("pasteuser:", err)
	}
	if err := keysMessages.Set(config.Config.Keymap.MessageShow, handleMessageCommand("show")); err != nil {
		PrintErrorMsg("message_show:", err)
	}
	if err := keysMessages.Set(config.Config.Keymap.MessageUrl, handleMessageCommand("url")); err != nil {
		PrintErrorMsg("message_url:", err)
	}
	if err := keysMessages.Set(config.Config.Keymap.MessageInfo, handleMessageCommand("info")); err != nil {
		PrintErrorMsg("message_info:", err)
	}
	if err := keysMessages.Set(config.Config.Keymap.MessageReact, handleMessageReact); err != nil {
		PrintErrorMsg("message_react:", err)
	}
	if err := keysMessages.Set(config.Config.Keymap.MessageRevoke, handleMessageCommand("revoke")); err != nil {
		PrintErrorMsg("message_revoke:", err)
	}
	keysMessages.SetKey(tcell.ModNone, tcell.KeyEscape, handleExitMessages)
	keysMessages.SetKey(tcell.ModNone, tcell.KeyUp, handleMessagesMove(-1))
	keysMessages.SetKey(tcell.ModNone, tcell.KeyDown, handleMessagesMove(1))
	keysMessages.SetKey(tcell.ModNone, tcell.KeyPgUp, handleMessagesMove(-10))
	keysMessages.SetKey(tcell.ModNone, tcell.KeyPgDn, handleMessagesMove(10))
	keysMessages.SetRune(tcell.ModNone, 'k', handleMessagesMove(-1))
	keysMessages.SetRune(tcell.ModNone, 'j', handleMessagesMove(1))
	keysMessages.SetRune(tcell.ModNone, 'g', handleMessagesFirst)
	keysMessages.SetRune(tcell.ModNone, 'G', handleMessagesLast)
	keysMessages.SetRune(tcell.ModCtrl, 'u', handleMessagesMove(-10))
	keysMessages.SetRune(tcell.ModCtrl, 'd', handleMessagesMove(10))
	textView.SetInputCapture(keysMessages.Capture)
	keysChatPanel := cbind.NewConfiguration()
	keysChatPanel.SetKey(tcell.ModNone, tcell.KeyEscape, handleExitChats)
	keysChatPanel.SetRune(tcell.ModCtrl, 'u', handleChatPanelUp)
	keysChatPanel.SetRune(tcell.ModCtrl, 'd', handleChatPanelDown)
	treeView.SetInputCapture(keysChatPanel.Capture)
}

// prints help to chat view
func PrintHelp() {
	cmdPrefix := config.Config.General.CmdPrefix
	fmt.Fprintln(textView, "[-::u]Keys:[-::U]")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "Global")
	fmt.Fprintln(textView, "[::b] Up/Down[::-] = Scroll history/chats")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.SwitchPanels, "[::-] = Switch input/chats")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.FocusMessages, "[::-] = Focus message panel")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.CommandQuit, "[::-] = Exit app")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[-::-]Input[-::-]")
	fmt.Fprintln(textView, "[::b] Enter[::-] = Send message")
	fmt.Fprintln(textView, "[::b] Shift+Enter[::-] = New line")
	fmt.Fprintln(textView, "[::b] Ctrl+Backspace / Ctrl+Delete[::-] = Delete word before / after the cursor")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[-::-]Emoji[-::-]")
	if config.Config.General.EmojiShortcodes {
		fmt.Fprintln(textView, "[::b] :name:[::-] = Type an emoji, e.g. :joy: gives 😂")
		fmt.Fprintln(textView, "[::b] :na[::-] = Show suggestions, Tab to select, Enter to insert, Esc to close")
		fmt.Fprintln(textView, " Recently used emoji are suggested first")
	} else {
		fmt.Fprintln(textView, " :name: shortcodes are off, see emoji_shortcodes in the config file")
	}
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[-::-]Message panel[-::-]")
	fmt.Fprintln(textView, "[::b] Up/Down[::-] = select message")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageDownload, "[::-] = Download attachment")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageOpen, "[::-] = Download & open attachment")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageShow, "[::-] = Download & show image using", config.Config.General.ShowCommand)
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageUrl, "[::-] = Find URL in message and open it")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageRevoke, "[::-] = Revoke message")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageReact, "[::-] = React to message, type the emoji after "+config.Config.General.CmdPrefix+"react")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageInfo, "[::-] = Info about message, including who reacted")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "Config file in ->", config.GetConfigFilePath())
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "Type [::b]"+cmdPrefix+"commands[::-] to see all commands")
	fmt.Fprintln(textView, "")
}

func PrintCommands() {
	cmdPrefix := config.Config.General.CmdPrefix
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[-::u]Commands:[-::U]")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[-::-]Global[-::-]")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"connect [::-]or[::b]", config.Config.Keymap.CommandConnect, "[::-] = (Re)Connect to server")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"disconnect[::-]  = Close the connection")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"logout[::-]  = Remove login data from computer")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"reset[::-]  = Remove stored session and reconnect cleanly")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"relink[::-]  = Link again to get the chat list from your phone")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"quit [::-]or[::b]", config.Config.Keymap.CommandQuit, "[::-] = Exit app")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[-::-]Chat[-::-]")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"backlog [::-]or[::b]", config.Config.Keymap.CommandBacklog, "[::-] = load next", config.Config.General.BacklogMsgQuantity, "previous messages")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"read [::-]or[::b]", config.Config.Keymap.CommandRead, "[::-] = mark new messages in chat as read")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"archive[::-] / [::b]"+cmdPrefix+"unarchive[::-]  = Archive or unarchive the chat, also on your phone")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"react[::-] emoji  = React to the selected message, without an emoji the reaction is removed")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"search[::-] text  = Search the loaded messages of the chat, or chats and groups when Chats is selected")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"search[::-]  = Show everything again")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"upload[::-] /path/to/file  = Upload any file as document")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"sendimage[::-] /path/to/file  = Send image message")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"sendvideo[::-] /path/to/file  = Send video message")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"sendaudio[::-] /path/to/file  = Send audio message")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[-::-]Groups[-::-]")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"leave[::-]  = Leave group")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"create[::-] [user-id[] [user-id[] Group Subject  = Create group with users")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"subject[::-] New Subject  = Change subject of group")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"add[::-] [user-id[]  = Add user to group")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"remove[::-] [user-id[]  = Remove user from group")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"admin[::-] [user-id[]  = Set admin role for user in group")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"removeadmin[::-] [user-id[]  = Remove admin role for user in group")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "Use[::b]", config.Config.Keymap.Copyuser, "[::-]to copy a selected user id to clipboard")
	fmt.Fprintln(textView, "Use[::b]", config.Config.Keymap.Pasteuser, "[::-]to paste clipboard to text input")
	fmt.Fprintln(textView, "")
}

// called when text is entered by the user
func EnterCommand(key tcell.Key) {
	if key == tcell.KeyEsc {
		// clear the input first, then the search results, then scroll the
		// chat back down to the newest messages
		reactTarget = ""
		if sndTxt != "" {
			setInput("")
		} else if messageSearch != "" || chatSearch != "" {
			Search("")
		} else {
			ResetMsgSelection()
		}
		return
	}
	if sndTxt == "" {
		return
	}
	cmdPrefix := config.Config.General.CmdPrefix
	if sndTxt == cmdPrefix+"help" {
		PrintHelp()
		setInput("")
		return
	}
	if sndTxt == cmdPrefix+"commands" {
		PrintCommands()
		setInput("")
		return
	}
	if sndTxt == cmdPrefix+"react" || strings.HasPrefix(sndTxt, cmdPrefix+"react ") {
		React(strings.TrimSpace(strings.TrimPrefix(sndTxt, cmdPrefix+"react")))
		setInput("")
		return
	}
	if sndTxt == cmdPrefix+"search" || strings.HasPrefix(sndTxt, cmdPrefix+"search ") {
		Search(strings.TrimSpace(strings.TrimPrefix(sndTxt, cmdPrefix+"search")))
		setInput("")
		return
	}
	if sndTxt == cmdPrefix+"quit" {
		sessionManager.CommandChannel <- messages.Command{"disconnect", nil}
		app.Stop()
		return
	}
	if strings.HasPrefix(sndTxt, cmdPrefix) {
		cmd := strings.TrimPrefix(sndTxt, cmdPrefix)
		var params []string
		if strings.Index(cmd, " ") >= 0 {
			cmdParts := strings.Split(cmd, " ")
			cmd = cmdParts[0]
			params = cmdParts[1:]
		}
		sessionManager.CommandChannel <- messages.Command{cmd, params}
		setInput("")
		return
	}
	if currentReceiver.Id == "" {
		PrintText("no receiver")
		setInput("")
		return
	}
	// no command, send as message
	msg := messages.Command{
		Name:   "send",
		Params: []string{currentReceiver.Id, replaceShortcodes(sndTxt)},
	}
	sessionManager.CommandChannel <- msg
	setInput("")
}

// get the next message id to select (highlighted + offset)
func GetOffsetMsgId(curId string, offset int) string {
	if curRegions == nil || len(curRegions) == 0 {
		return ""
	}
	for idx, val := range curRegions {
		if val.Id == curId {
			arrPos := idx + offset
			if len(curRegions) > arrPos && arrPos >= 0 {
				return curRegions[arrPos].Id
			}
		}
	}
	if offset > 0 {
		return curRegions[0].Id
	} else {
		return curRegions[len(curRegions)-1].Id
	}
}

// resets the selection in the textView and scrolls it down
func ResetMsgSelection() {
	if len(textView.GetHighlights()) > 0 {
		textView.Highlight("")
	}
	textView.ScrollToEnd()
}

// prints text to the TextView
func PrintText(txt string) {
	fmt.Fprintln(textView, txt)
}

// prints an error to the TextView
func PrintError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(textView, "["+config.Config.Colors.Negative+"]", err.Error(), "[-]")
}

// prints an error to the TextView
func PrintErrorMsg(text string, err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(textView, "["+config.Config.Colors.Negative+"]", text, err.Error(), "[-]")
}

// prints an image attachment to the TextView (by message id)
func PrintImage(path string) {
	var err error
	cmdParts := strings.Split(config.Config.General.ShowCommand, " ")
	cmdParts = append(cmdParts, path)
	var cmd *exec.Cmd
	size := len(cmdParts)
	if size > 1 {
		cmd = exec.Command(cmdParts[0], cmdParts[1:]...)
	} else if size > 0 {
		cmd = exec.Command(cmdParts[0])
	}
	var stdout io.ReadCloser
	if stdout, err = cmd.StdoutPipe(); err == nil {
		if err = cmd.Start(); err == nil {
			reader := bufio.NewReader(stdout)
			io.Copy(tview.ANSIWriter(textView), reader)
			return
		}
	}
	PrintError(err)
}

// updates the status bar
func UpdateStatusBar(statusInfo messages.SessionStatus) {
	lastStatus = statusInfo
	out := " "
	if statusInfo.Connected {
		out += "[" + config.Config.Colors.Positive + "]online[-]"
	} else {
		out += "[" + config.Config.Colors.Negative + "]offline[-]"
	}
	out += " "
	// the status bar is narrow, show what whatscli is waiting for instead of the rest
	if statusInfo.Activity != "" {
		infoBar.SetText(out + "[::d]" + statusInfo.Activity + "[::-]")
		return
	}
	out += statusInfo.LastSeen
	out += syncText(statusInfo.Connected, sessionManager.LastReceived(), time.Now())
	infoBar.SetText(out)
}

// the status last shown in the status bar, shown again every second, see syncText
var lastStatus messages.SessionStatus

// syncText tells how long ago data was last received from WhatsApp. It is red
// when that was over a minute ago while connected, as the answers to keepalive
// pings come every 20 to 30 seconds, and the connection seems to be lost.
func syncText(connected bool, lastReceived time.Time, now time.Time) string {
	if lastReceived.IsZero() {
		return ""
	}
	since := now.Sub(lastReceived)
	color := "gray"
	if connected && since > time.Minute {
		color = config.Config.Colors.Negative
	}
	return "[" + color + "]synced " + timeAgo(since) + "[-]"
}

// timeAgo returns how long ago something was, like "just now" or "5 min ago"
func timeAgo(duration time.Duration) string {
	plural := func(count int, unit string) string {
		if count == 1 {
			return fmt.Sprintf("1 %s ago", unit)
		}
		return fmt.Sprintf("%d %ss ago", count, unit)
	}
	switch {
	case duration < time.Minute:
		return "just now"
	case duration < time.Hour:
		return fmt.Sprintf("%d min ago", int(duration.Minutes()))
	case duration < 24*time.Hour:
		return plural(int(duration.Hours()), "hour")
	default:
		return plural(int(duration.Hours()/24), "day")
	}
}

// what was typed in the input for each chat, kept while switching chats
var drafts = map[string]string{}

// switchDraft keeps the text typed for the chat that is left, and returns the
// text typed earlier for the chat that is opened, which is in the input again
// and marked as a draft no more
func switchDraft(from string, to string, typed string) string {
	draft := drafts[to]
	delete(drafts, to)
	if typed == "" {
		delete(drafts, from)
	} else {
		drafts[from] = typed
	}
	return draft
}

// sets the current chat, loads text from storage to TextView
func SetDisplayedChat(wid messages.Chat) {
	//TODO: how to get chat to set
	if wid.Id != currentReceiver.Id {
		setInput(switchDraft(currentReceiver.Id, wid.Id, textInput.GetText()))
		updateChatNode(currentReceiver.Id)
		updateChatNode(wid.Id)
		// the message to react to is in the other chat
		reactTarget = ""
	}
	currentReceiver = wid
	chatMessages = nil
	messageSearch = ""
	textView.Clear()
	textView.SetTitle(wid.Name)
	sessionManager.CommandChannel <- messages.Command{"select", []string{currentReceiver.Id}}
}

// get a string representation of all messages for chat
func getMessagesString(msgs []messages.Message) string {
	out := ""
	for idx := range msgs {
		var prev *messages.Message
		if idx > 0 && messageSearch == "" {
			prev = &msgs[idx-1]
		}
		out += getTextMessageString(&msgs[idx], prev)
		out += "\n"
	}
	return out
}

// messageGroupGap is how close in time messages of one sender must follow each
// other to be shown as a group, with the time and name only on the first one
const messageGroupGap = 2 * time.Minute

// continuesGroup returns whether msg is shown in the group of the message before it
func continuesGroup(prev *messages.Message, msg *messages.Message) bool {
	if prev == nil || prev.FromMe != msg.FromMe || (!msg.FromMe && prev.ContactId != msg.ContactId) {
		return false
	}
	gap := time.Duration(int64(msg.Timestamp)-int64(prev.Timestamp)) * time.Second
	return gap >= 0 && gap <= messageGroupGap
}

// reactionSummary returns the emoji reactions to a message, the most given
// first, with how often they were given if more than once
func reactionSummary(reactions map[string]string) string {
	counts := make(map[string]int)
	for _, reaction := range reactions {
		counts[reaction]++
	}
	emojis := make([]string, 0, len(counts))
	for emoji := range counts {
		emojis = append(emojis, emoji)
	}
	sort.Slice(emojis, func(i, j int) bool {
		if counts[emojis[i]] != counts[emojis[j]] {
			return counts[emojis[i]] > counts[emojis[j]]
		}
		return emojis[i] < emojis[j]
	})
	parts := make([]string, len(emojis))
	for idx, emoji := range emojis {
		parts[idx] = tview.Escape(emoji)
		if counts[emoji] > 1 {
			parts[idx] += fmt.Sprint(counts[emoji])
		}
	}
	return strings.Join(parts, " ")
}

// formatMessageTime returns a short time for a message, with as much of the date
// as is needed to tell when it was sent
func formatMessageTime(sent time.Time, now time.Time) string {
	sentDay := time.Date(sent.Year(), sent.Month(), sent.Day(), 0, 0, 0, 0, now.Location())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch {
	case !sentDay.Before(today):
		return sent.Format("15:04")
	case sentDay.After(today.AddDate(0, 0, -7)):
		return sent.Format("Mon 15:04")
	case sent.Year() == now.Year():
		return sent.Format("2 Jan 15:04")
	default:
		return sent.Format("2 Jan 2006")
	}
}

// create a formatted string with regions based on message ID from a text message
//TODO: optimize, use Sprintf etc
func getTextMessageString(msg *messages.Message, prev *messages.Message) string {
	colorMe := config.Config.Colors.ChatMe
	colorContact := config.Config.Colors.ChatContact
	out := ""
	text := formatMarkup(msg.Text, messageSearch)
	if msg.Forwarded {
		text = "[" + config.Config.Colors.ForwardedText + "]" + text + "[-]"
	}
	// the time and name are shown on their own line, once for each group, with
	// an empty line between groups
	header := ""
	if !continuesGroup(prev, msg) {
		if prev != nil {
			header = "\n"
		}
		header += "[gray::-](" + formatMessageTime(time.Unix(int64(msg.Timestamp), 0), time.Now()) + ") "
		if msg.FromMe { //msg from me
			header += "[" + colorMe + "::b]Me:[-::-]\n"
		} else { // message from others
			header += "[" + colorContact + "::b]" + msg.ContactShort + ":[-::-]\n"
		}
	}
	out += "[\""
	out += msg.Id
	out += "\"]"
	out += header + text
	// marked so they can't be mistaken for a message that is only an emoji
	if reactions := reactionSummary(msg.Reactions); reactions != "" {
		out += "\n[gray::-] ↳" + reactions + "[-::-]"
	}
	out += "[\"\"]"
	return out
}

type UiHandler struct{}

func (u UiHandler) NewMessage(msg messages.Message) {
	//TODO: its stupid to "go" this as its supposed to run
	//on the ui thread anyway. But QueueUpdate blocks...?
	go app.QueueUpdateDraw(func() {
		chatMessages = append(chatMessages, msg)
		if !messageMatches(msg, messageSearch) {
			return
		}
		var prev *messages.Message
		if len(curRegions) > 0 && messageSearch == "" {
			prev = &curRegions[len(curRegions)-1]
		}
		PrintText(getTextMessageString(&msg, prev))
		curRegions = append(curRegions, msg)
	})
}

func (u UiHandler) NewScreen(msgs []messages.Message) {
	go app.QueueUpdateDraw(func() {
		chatMessages = msgs
		renderMessages()
	})
}

// shows the messages of the displayed chat, or the ones matching the search
func renderMessages() {
	textView.Clear()
	shown := chatMessages
	if messageSearch != "" {
		shown = nil
		for _, msg := range chatMessages {
			if messageMatches(msg, messageSearch) {
				shown = append(shown, msg)
			}
		}
		PrintText(fmt.Sprintf("[::d]%d of %d loaded messages contain \"%s\", press %s to load older ones, %ssearch to show all[::-]\n",
			len(shown), len(chatMessages), tview.Escape(messageSearch), config.Config.Keymap.CommandBacklog, config.Config.General.CmdPrefix))
	}
	screen := getMessagesString(shown)
	fmt.Fprint(textView, screen)
	curRegions = shown
	if screen == "" && messageSearch == "" {
		if currentReceiver.Id == "" {
			PrintHelp()
		} else {
			PrintText("[::d] ~~~ no messages, press " + config.Config.Keymap.CommandBacklog + " to load backlog if available ~~~[::-]")
		}
	}
}

// Search filters the chat list when Chats is selected, or the messages of the
// displayed chat. An empty text shows everything again.
func Search(text string) {
	if currentReceiver.Id == "" || (text == "" && messageSearch == "") {
		chatSearch = text
		renderChats()
		if text != "" {
			// Down selects the first result
			treeView.SetCurrentNode(chatRoot)
			app.SetFocus(treeView)
		}
		return
	}
	// after showing everything again, stay at the selected search result
	selected := textView.GetHighlights()
	messageSearch = text
	renderMessages()
	if text == "" && len(selected) > 0 {
		textView.Highlight(selected[0])
		textView.ScrollToHighlight()
	} else {
		textView.ScrollToEnd()
	}
}

// messageMatches returns whether the text of a message contains the search, ignoring case
func messageMatches(msg messages.Message, search string) bool {
	return search == "" || strings.Contains(strings.ToLower(msg.Text), strings.ToLower(search))
}

// chatMatches returns whether the name or number of a chat contains the search, ignoring case
func chatMatches(chat messages.Chat, search string) bool {
	search = strings.ToLower(search)
	return strings.Contains(strings.ToLower(chat.Name), search) || strings.Contains(strings.Split(chat.Id, "@")[0], search)
}

// the search compiled for highlighting, compiled again when the search changes
var searchPattern struct {
	search  string
	pattern *regexp.Regexp
}

// highlightSearch escapes text for the message panel, and highlights where it contains the search
func highlightSearch(text string, search string) string {
	if search == "" {
		return tview.Escape(text)
	}
	if searchPattern.pattern == nil || searchPattern.search != search {
		searchPattern.search = search
		searchPattern.pattern = regexp.MustCompile("(?i)" + regexp.QuoteMeta(search))
	}
	out := ""
	last := 0
	for _, match := range searchPattern.pattern.FindAllStringIndex(text, -1) {
		out += tview.Escape(text[last:match[0]]) + "[black:yellow]" + tview.Escape(text[match[0]:match[1]]) + "[-:-]"
		last = match[1]
	}
	return out + tview.Escape(text[last:])
}

// loads the chat data from storage to the TreeView
func (u UiHandler) SetChats(ids []messages.Chat) {
	go app.QueueUpdateDraw(func() {
		allChats = ids
		renderChats()
	})
}

// shows the chats in the chat list, or the ones matching the search, including
// archived chats and contacts without messages
func renderChats() {
	chatRoot.ClearChildren()
	archivedNode := setNodeColor(tview.NewTreeNode("Archived"), tcell.ColorNames[config.Config.Colors.ListHeader]).
		SetReference("archived").
		SetSelectable(true).
		SetExpanded(archivedExpanded)
	oldId := currentReceiver.Id
	for _, element := range allChats {
		if chatSearch != "" {
			if !chatMatches(element, chatSearch) {
				continue
			}
		} else if element.Hidden {
			continue
		}
		node := tview.NewTreeNode(chatNodeText(element)).
			SetReference(element).
			SetSelectable(true)
		if element.IsGroup {
			setNodeColor(node, tcell.ColorNames[config.Config.Colors.ListGroup])
		} else {
			setNodeColor(node, tcell.ColorNames[config.Config.Colors.ListContact])
		}
		// store new currentReceiver, else the selection on the left goes off
		if element.Id == oldId {
			currentReceiver = element
		}
		if element.InArchive && chatSearch == "" {
			archivedNode.AddChild(node)
		} else {
			chatRoot.AddChild(node)
		}
		if element.Id == currentReceiver.Id {
			if element.InArchive {
				archivedExpanded = true
				archivedNode.SetExpanded(true)
			}
			treeView.SetCurrentNode(node)
		}
	}
	if count := len(archivedNode.GetChildren()); count > 0 {
		archivedNode.SetText(fmt.Sprintf("Archived (%d)", count))
		chatRoot.AddChild(archivedNode)
	}
	if chatSearch != "" {
		chatRoot.SetText(fmt.Sprintf("Chats with \"%s\" (%d)", tview.Escape(chatSearch), len(chatRoot.GetChildren())))
	} else {
		chatRoot.SetText("Chats")
	}
}

// chatNodeText returns the text of a chat in the chat list
func chatNodeText(chat messages.Chat) string {
	name := chat.Name
	if name == "" {
		name = strings.TrimSuffix(strings.TrimSuffix(chat.Id, messages.GROUPSUFFIX), messages.CONTACTSUFFIX)
	}
	if chat.Pinned {
		name = "📌 " + name
	}
	if drafts[chat.Id] != "" {
		name += " ✎"
	}
	if chat.Unread > 0 {
		// bold, so that isUnreadCount can tell it from headers in the same color
		name += " ([" + config.Config.Colors.UnreadCount + "::b]" + fmt.Sprint(chat.Unread) + "[-::-])"
	}
	// search results show archived chats among the others
	if chat.InArchive && chatSearch != "" {
		name += " [::d](archived)[::-]"
	}
	return name
}

// updateChatNode updates the text of a chat in the chat list, e.g. when its draft changed
func updateChatNode(chatID string) {
	chatRoot.Walk(func(node, parent *tview.TreeNode) bool {
		if chat, ok := node.GetReference().(messages.Chat); ok && chat.Id == chatID {
			node.SetText(chatNodeText(chat))
		}
		return true
	})
}

func (u UiHandler) PrintError(err error) {
	PrintError(err)
}

func (u UiHandler) PrintText(msg string) {
	PrintText(msg)
}

func (u UiHandler) PrintFile(path string) {
	go app.QueueUpdateDraw(func() {
		PrintImage(path)
	})
}

func (u UiHandler) OpenFile(path string) {
	open.Run(path)
}

func (u UiHandler) SetStatus(status messages.SessionStatus) {
	go app.QueueUpdateDraw(func() {
		UpdateStatusBar(status)
	})
}

func (u UiHandler) GetWriter() io.Writer {
	return textView
}
