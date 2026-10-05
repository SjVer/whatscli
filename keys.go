package main

import (
	_ "embed"

	"codeberg.org/tslocum/cbind"
	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

// scrollMessages scrolls the messages by lines, up for a negative number.
// At the end, it follows new messages again.
func scrollMessages(lines int) {
	offset, _ := textView.GetScrollOffset()
	_, _, _, height := textView.GetInnerRect()
	if lines > 0 && offset+lines >= textView.GetWrappedLineCount()-height {
		textView.ScrollToEnd()
		return
	}
	textView.ScrollTo(max(0, offset+lines), 0)
}

// scrollLines returns a number of lines to scroll from the config, at least one
func scrollLines(configured int) int {
	return max(1, configured)
}

// scrollWithWheel scrolls the messages by the configured lines with the mouse wheel
func scrollWithWheel(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
	switch action {
	case tview.MouseScrollUp:
		scrollMessages(-scrollLines(config.Config.Ui.ScrollLines))
	case tview.MouseScrollDown:
		scrollMessages(scrollLines(config.Config.Ui.ScrollLines))
	default:
		return action, event
	}
	return tview.MouseConsumed, nil
}

func handleFocusMessage(ev *tcell.EventKey) *tcell.EventKey {
	if !textView.HasFocus() {
		app.SetFocus(textView)
		if len(curRegions) > 0 {
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

func handleFocusChats(ev *tcell.EventKey) *tcell.EventKey {
	ResetMsgSelection()
	if !treeView.HasFocus() {
		app.SetFocus(treeView)
	}
	return nil
}

func handleSwitchPanels(ev *tcell.EventKey) *tcell.EventKey {
	// Tab cycles the emoji suggestions while they are shown
	if textInput.HasFocus() && handleSuggestionKeys(ev) {
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
		sendCommand(messages.Command{Name: command})
		return nil
	}
}

// copies the ID of who sent the selected message, or of the open chat
func handleCopyUser(ev *tcell.EventKey) *tcell.EventKey {
	id, name := currentReceiver.Id, currentReceiver.Name
	if hls := textView.GetHighlights(); len(hls) > 0 {
		for _, msg := range curRegions {
			if msg.Id == hls[0] {
				id, name = msg.ContactId, msg.ContactName
			}
		}
		ResetMsgSelection()
	}
	if id == "" {
		return nil
	} else if err := writeClipboard(id); err != nil {
		PrintErrorMsg("failed to copy:", err)
	} else {
		PrintHint("copied id of " + name + " to clipboard")
	}
	return nil
}

// pastes the text on the clipboard after the typed text
func handlePasteUser(ev *tcell.EventKey) *tcell.EventKey {
	if clip, err := readClipboard(); err == nil {
		setInput(textInput.GetText() + " " + clip)
	} else {
		PrintErrorMsg("failed to paste:", err)
	}
	return nil
}

func handleQuit(ev *tcell.EventKey) *tcell.EventKey {
	sendCommand(messages.Command{Name: "disconnect"})
	app.Stop()
	return nil
}

func handleHelp(ev *tcell.EventKey) *tcell.EventKey {
	PrintHelp()
	printedSinceRender.Store(true)
	return nil
}

// handleMessageCommand runs a command on the selected message. Commands that
// open or save it keep the place in the chat and the selection, the others,
// like the info that is printed below the messages, go back to the input.
func handleMessageCommand(command string) func(ev *tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		hls := textView.GetHighlights()
		if len(hls) > 0 {
			sendCommand(messages.Command{Name: command, Params: []string{hls[0]}})
			if !keepsPlace[command] {
				ResetMsgSelection()
				app.SetFocus(textInput)
			}
		}
		return nil
	}
}

// keepsPlace are the message commands that keep the place in the chat, see handleMessageCommand
var keepsPlace = map[string]bool{"download": true, "open": true, "url": true}

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

func handleMessagesMove(amount int) func(ev *tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		if len(curRegions) == 0 {
			return nil
		}
		hls := textView.GetHighlights()
		if len(hls) > 0 {
			newId := GetOffsetMsgId(hls[0], amount)
			if newId != "" {
				textView.Highlight(newId)
			}
		} else {
			// at the newest, at the bottom
			textView.Highlight(curRegions[len(curRegions)-1].Id)
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
	showChatsRoot()
	return nil
}

// archives the selected chat, or unarchives it when it is archived
func handleChatArchive(ev *tcell.EventKey) *tcell.EventKey {
	chat, ok := treeView.GetCurrentNode().GetReference().(messages.Chat)
	if !ok {
		return nil // Chats or the archived chats folder
	}
	command := "archive"
	if chat.InArchive {
		command = "unarchive"
	}
	sendCommand(messages.Command{Name: command, Params: []string{chat.Id}})
	return nil
}

func handleMessagesLast(ev *tcell.EventKey) *tcell.EventKey {
	selectMessage(len(curRegions) - 1)
	return nil
}

func handleMessagesFirst(ev *tcell.EventKey) *tcell.EventKey {
	selectMessage(0)
	return nil
}

// selectMessage selects the shown message at the index, if there is one
func selectMessage(idx int) {
	if idx >= 0 && idx < len(curRegions) {
		textView.Highlight(curRegions[idx].Id)
		textView.ScrollToHighlight()
	}
}

func handleExitMessages(ev *tcell.EventKey) *tcell.EventKey {
	if messageSearch != "" {
		Search("")
		return nil
	}
	ResetMsgSelection()
	app.SetFocus(textInput)
	return nil
}

// load the key map
func LoadShortcuts() {
	keymap := config.Config.Keymap
	handler := func(command string) keyHandler { return handleCommand(command) }
	app.SetInputCapture(bindKeys(cbind.NewConfiguration(), []keyBinding{
		{keymap.FocusMessages, "focus_messages", handleFocusMessage},
		{keymap.FocusInput, "focus_input", handleFocusInput},
		{keymap.FocusChats, "focus_chats", handleFocusChats},
		{keymap.SwitchPanels, "switch_panels", handleSwitchPanels},
		{keymap.CommandRead, "command_read", handler("read")},
		{keymap.Copyuser, "copyuser", handleCopyUser},
		{keymap.Pasteuser, "pasteuser", handlePasteUser},
		{keymap.CommandBacklog, "command_backlog", handler("backlog")},
		{keymap.CommandConnect, "command_connect", handler("login")},
		{keymap.CommandQuit, "command_quit", handleQuit},
		{keymap.CommandHelp, "command_help", handleHelp},
		{keymap.PasteImage, "paste_image", handlePasteImage},
	}).Capture)

	// the message panel, the fixed keys first, so that the configured ones replace them
	keysMessages := cbind.NewConfiguration()
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
	textView.SetInputCapture(bindKeys(keysMessages, []keyBinding{
		{keymap.MessageDownload, "message_download", handleMessageCommand("download")},
		{keymap.MessageOpen, "message_open", handleMessageCommand("open")},
		{keymap.MessageUrl, "message_url", handleMessageCommand("url")},
		{keymap.MessageInfo, "message_info", handleMessageCommand("info")},
		{keymap.MessageReact, "message_react", handleMessageReact},
		{keymap.MessageReply, "message_reply", handleMessageReply},
		{keymap.MessageRevoke, "message_revoke", handleMessageCommand("revoke")},
	}).Capture)

	keysChatPanel := cbind.NewConfiguration()
	keysChatPanel.SetKey(tcell.ModNone, tcell.KeyEscape, handleExitChats)
	treeView.SetInputCapture(bindKeys(keysChatPanel, []keyBinding{
		{keymap.ChatArchive, "chat_archive", handleChatArchive},
	}).Capture)
}

// keyHandler handles a key, returning nil when it was handled
type keyHandler = func(*tcell.EventKey) *tcell.EventKey

// keyBinding is a key from the config, its name there, and what it does
type keyBinding struct {
	key     string
	name    string
	handler keyHandler
}

// bindKeys adds the bindings to keys, and tells which keys in the config are wrong
func bindKeys(keys *cbind.Configuration, bindings []keyBinding) *cbind.Configuration {
	for _, binding := range bindings {
		if err := keys.Set(binding.key, binding.handler); err != nil {
			PrintErrorMsg(binding.name+":", err)
		}
	}
	return keys
}

// get the next message id to select (highlighted + offset)
func GetOffsetMsgId(curId string, offset int) string {
	if len(curRegions) == 0 {
		return ""
	}
	for idx, val := range curRegions {
		if val.Id == curId {
			// stays at the first or last message, instead of going round
			return curRegions[max(0, min(idx+offset, len(curRegions)-1))].Id
		}
	}
	// the selected one isn't shown anymore
	return curRegions[len(curRegions)-1].Id
}

// resets the selection in the textView and scrolls it down
func ResetMsgSelection() {
	if len(textView.GetHighlights()) > 0 {
		textView.Highlight("")
	}
	textView.ScrollToEnd()
}
