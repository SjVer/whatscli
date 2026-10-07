package main

import (
	_ "embed"
	"fmt"

	"github.com/normen/whatscli/config"
)

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
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.PasteImage, "[::-] = Attach the image on the clipboard, the typed text is its caption")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[-::-]Emoji[-::-]")
	if config.Config.General.EmojiShortcodes {
		fmt.Fprintln(textView, "[::b] :name:[::-] = Type an emoji, e.g. :joy: gives 😂")
		fmt.Fprintln(textView, "[::b] :na[::-] = Show suggestions, Tab to select, Enter to insert, Esc to close")
		fmt.Fprintln(textView, " Recently used emoji are suggested first")
	} else {
		fmt.Fprintln(textView, " :name: shortcodes are off, see emoji_shortcodes in the config file")
	}
	fmt.Fprintln(textView, "[::b] @na[::-] = Mention a group member, suggested like emoji")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[-::-]Message panel[-::-]")
	fmt.Fprintln(textView, "[::b] Up/Down[::-] = select message")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageDownload, "[::-] = Download attachment")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageOpen, "[::-] = Download & open attachment with the default app")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageUrl, "[::-] = Find URL in message and open it")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageRevoke, "[::-] = Revoke one of your messages, for everyone")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageReact, "[::-] = React to message, type the emoji after "+config.Config.General.CmdPrefix+"react")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageReply, "[::-] = Reply to message, type the reply in the input")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.MessageInfo, "[::-] = Info about message, including who reacted")
	fmt.Fprintln(textView, " Images and stickers get alt texts by a local model, see alt_text_model in the README")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[-::-]Chat list[-::-]")
	fmt.Fprintln(textView, "[::b]", config.Config.Keymap.ChatArchive, "[::-] = Archive or unarchive the selected chat, also on your phone")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "Config file in ->", config.GetConfigFilePath())
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "Type [::b]"+cmdPrefix+"commands[::-] to see all commands")
	fmt.Fprintln(textView, "")
}

func PrintCommands() {
	printedSinceRender.Store(true)
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
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"code[::-] phone-number  = Link with a code instead of the QR code, while it is shown")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"quit [::-]or[::b]", config.Config.Keymap.CommandQuit, "[::-] = Exit app")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[-::-]Chat[-::-]")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"backlog [::-]or[::b]", config.Config.Keymap.CommandBacklog, "[::-] = load next", config.Config.General.BacklogMsgQuantity, "previous messages")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"read [::-]or[::b]", config.Config.Keymap.CommandRead, "[::-] = mark new messages in chat as read")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"recap[::-] time  = Sum up the messages of a time, like 10m, 2h or 3 days, with the AI model, without a time the unread ones")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"ask[::-] time question  = Answer a question about the messages of a time with the AI model, like "+cmdPrefix+"ask 5d what did we plan for sunday?")
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
