package main

import (
	_ "embed"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
)

// newTextInput creates the input for messages and commands
func newTextInput() *tview.TextArea {
	input := tview.NewTextArea()
	input.SetBackgroundColor(tcell.ColorNames[config.Config.Colors.Background])
	input.SetTextStyle(tcell.StyleDefault.
		Background(tcell.ColorNames[config.Config.Colors.InputBackground]).
		Foreground(tcell.ColorNames[config.Config.Colors.InputText]))
	input.SetChangedFunc(func() {
		updateInputColor()
		updateSuggestions()
	})
	input.SetMovedFunc(updateSuggestions)
	input.SetInputCapture(handleInputKeys)
	suggestionPopup.dismissed = -1
	return input
}

// handles keys of the input field before it does
func handleInputKeys(event *tcell.EventKey) *tcell.EventKey {
	if handleSuggestionKeys(event) {
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
		} else if event.Key() == tcell.KeyUp {
			scrollMessages(-scrollLines(config.Config.Ui.ScrollLines))
		} else {
			scrollMessages(scrollLines(config.Config.Ui.ScrollLines))
		}
		return nil
	case tcell.KeyPgUp:
		scrollMessages(-scrollLines(config.Config.Ui.PageLines))
		return nil
	case tcell.KeyPgDn:
		scrollMessages(scrollLines(config.Config.Ui.PageLines))
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
	// the input doesn't call its changed func for this
	updateInputColor()
	updateSuggestions()
}

// internalCommands are commands of the session manager that the UI gives, which
// can't be typed: they would change the open chat or send behind its back
var internalCommands = map[string]bool{"select": true, "send": true, "reply": true, "pasteimage": true}

// sendingCommands put something into the chat, unlike the silent commands
var sendingCommands = map[string]bool{
	"send": true, "reply": true, "upload": true, "sendimage": true, "sendvideo": true, "sendaudio": true, "react": true,
}

// parseCommand returns whether text is a command, and whether it is silent,
// sending nothing to the chat
func parseCommand(text string) (isCommand bool, silent bool) {
	name, _, isCommand := cutCommand(text)
	return isCommand, isCommand && !sendingCommands[name]
}

// cutCommand returns the name of the command typed in text and what follows
// it, and whether text is a command
func cutCommand(text string) (string, string, bool) {
	command, ok := strings.CutPrefix(text, config.Config.General.CmdPrefix)
	name, args, _ := strings.Cut(command, " ")
	return name, args, ok
}

// updateInputColor shows commands in their own colors, silent ones apart
func updateInputColor() {
	color := config.Config.Colors.InputText
	if isCommand, silent := parseCommand(textInput.GetText()); silent {
		color = config.Config.Colors.SilentCommandText
	} else if isCommand {
		color = config.Config.Colors.CommandText
	}
	textInput.SetTextStyle(textInput.GetTextStyle().Foreground(tcell.ColorNames[color]))
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
		PrintHint("select a message first: " + config.Config.Keymap.FocusMessages + " and up/down, then " + config.Config.Keymap.MessageReact)
		return
	}
	sendCommand(messages.Command{Name: "react", Params: []string{target, replaceShortcodes(emoji)}})
	ResetMsgSelection()
}

// called when text is entered by the user
func EnterCommand(key tcell.Key) {
	text := textInput.GetText()
	if key == tcell.KeyEsc {
		// clear the input first, then the attached image and the reply, then
		// the search results, then scroll the chat back down to the newest messages
		reactTarget = ""
		if text != "" {
			setInput("")
		} else if pastedImage != "" {
			removePastedImage()
		} else if replyTarget != "" {
			cancelReply()
		} else if messageSearch != "" || chatSearch != "" {
			Search("")
		} else {
			ResetMsgSelection()
		}
		return
	}
	if text == "" && pastedImage == "" {
		return
	}
	name, args, isCommand := cutCommand(text)
	if !isCommand && currentReceiver.Id == "" {
		PrintHint("open a chat to send a message") // and the text stays
		return
	}
	defer setInput("")
	if !isCommand {
		sendMessage(text)
		return
	}
	if internalCommands[name] {
		PrintHint(config.Config.General.CmdPrefix + name + " isn't a command, see " + config.Config.General.CmdPrefix + "commands")
		return
	}
	// commands of the UI, the others are run by the session manager
	switch name {
	case "help":
		handleHelp(nil)
	case "commands":
		PrintCommands()
	case "react":
		React(strings.TrimSpace(args))
	case "search":
		Search(strings.TrimSpace(args))
	case "quit":
		handleQuit(nil)
	case "code":
		// while the session manager waits for the phone to link
		sessionManager.LinkWithCode(args)
	default:
		var params []string
		if args != "" {
			// split at single spaces, which keeps the spaces and lines of a message
			params = strings.Split(args, " ")
		}
		sendCommand(messages.Command{Name: name, Params: params})
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

// sendCommand gives a command to the session manager, without waiting for it,
// which can be busy, e.g. waiting for the phone to link
func sendCommand(command messages.Command) {
	select {
	case sessionManager.CommandChannel <- command:
	default:
		PrintHint("whatscli is busy, e.g. waiting for the phone to link, try again in a moment")
	}
}
