package main

import (
	_ "embed"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

// PrintHint prints a short answer of whatscli, like that something can't be
// done, dim so that it isn't taken for a message
func PrintHint(text string) {
	PrintText("[::d]" + tview.Escape(text) + "[::-]")
}

// prints text to the TextView
func PrintText(txt string) {
	fmt.Fprintln(textView, txt)
	printedSinceRender.Store(true)
}

// prints an error to the TextView
func PrintError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(textView, "["+config.Config.Colors.Negative+"]", tview.Escape(err.Error()), "[-]")
	printedSinceRender.Store(true)
}

// prints an error to the TextView
func PrintErrorMsg(text string, err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(textView, "["+config.Config.Colors.Negative+"]", tview.Escape(text), tview.Escape(err.Error()), "[-]")
	printedSinceRender.Store(true)
}

// getMessagesString returns the messages as they are shown in the message
// panel of the width, see getTextMessageString
func getMessagesString(msgs []messages.Message, width int) string {
	// the messages and, unless searching, the reactions to them by when they were given
	type chatLine struct {
		msg     *messages.Message
		reactor string
		at      int64
	}
	lines := make([]chatLine, 0, len(msgs))
	for idx := range msgs {
		msg := &msgs[idx]
		lines = append(lines, chatLine{msg: msg, at: int64(msg.Timestamp)})
		if messageSearch != "" {
			continue
		}
		reactors := make([]string, 0, len(msg.ReactionTimes))
		for reactor := range msg.ReactionTimes {
			if msg.Reactions[reactor] != "" {
				reactors = append(reactors, reactor)
			}
		}
		sort.Strings(reactors)
		for _, reactor := range reactors {
			lines = append(lines, chatLine{msg: msg, reactor: reactor, at: msg.ReactionTimes[reactor]})
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].at < lines[j].at })

	out := ""
	var prev *messages.Message
	for idx, line := range lines {
		if line.reactor != "" {
			// an empty line before the first of several reactions
			if idx > 0 && prev != &afterReaction {
				out += "\n"
			}
			out += getReactionString(line.msg, line.reactor, line.at) + "\n"
			prev = &afterReaction
			continue
		}
		if messageSearch != "" {
			prev = nil
		}
		out += getTextMessageString(line.msg, prev, width) + "\n"
		prev = line.msg
	}
	endedWithReaction = prev == &afterReaction
	return out
}

// afterReaction is the message before a message that follows a reaction line,
// which starts a new group, see continuesGroup
var afterReaction = messages.Message{ContactId: "reaction"}

// whether the last line shown in the chat is a reaction
var endedWithReaction bool

// reactorName returns the name to show for who reacted
var reactorName = func(reactor string) string {
	if sessionManager == nil {
		return reactor
	}
	return sessionManager.ShortName(reactor)
}

// getReactionString returns a dimmed line telling who reacted to a message with what
func getReactionString(msg *messages.Message, reactor string, at int64) string {
	return "[::d](" + formatMessageTime(time.Unix(at, 0), time.Now()) + ") " + nameText(reactor, reactorName(reactor)) +
		" reacted " + tview.Escape(msg.Reactions[reactor]) + " to \"" + tview.Escape(excerpt(msg.Text, 40)) + "\"[::-]"
}

// messageGroupGap is how close in time messages of one sender must follow each
// other to be shown as a group, with the time and name only on the first one
const messageGroupGap = 2 * time.Minute

// hasUnreadReaction returns whether one of the reactions to a message in the
// chat is new, see messages.Chat.UnreadReactions
func hasUnreadReaction(msg *messages.Message, chat messages.Chat) bool {
	for _, at := range msg.ReactionTimes {
		if slices.Contains(chat.UnreadReactions, at) {
			return true
		}
	}
	return false
}

// continuesGroup returns whether msg is shown in the group of the message before it
func continuesGroup(prev *messages.Message, msg *messages.Message) bool {
	// unread messages start a group, so that their time shows they are unread
	if prev == nil || prev.FromMe != msg.FromMe || (!msg.FromMe && prev.ContactId != msg.ContactId) || prev.Unread != msg.Unread {
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

// formatText returns the text of a message as it is shown, with the label of
// media, like [IMAGE: a dog], in its own color, apart from the caption
func formatText(msg *messages.Message) string {
	text := messages.WithAltText(msg.Text, msg.AltText)
	label, rest := messages.SplitLabel(text)
	if msg.Kind == messages.MessageKindText || label == "" {
		return formatMarkup(text, messageSearch, msg.Mentions)
	}
	return "[" + config.Config.Colors.MediaLabel + "]" + highlightSearch(label, messageSearch) + "[-]" + formatMarkup(rest, messageSearch, msg.Mentions)
}

// getTextMessageString returns a message as it is shown, in a region with its
// ID, with the time and name when it doesn't continue the group of prev. The ticks of the user's messages are placed for the width of the message
// panel, none for 0, see withTicks.
func getTextMessageString(msg *messages.Message, prev *messages.Message, width int) string {
	colorMe := config.Config.Colors.ChatMe
	colorContact := config.Config.Colors.ChatContact
	out := ""
	text := formatText(msg)
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
		timeColor := "gray"
		if msg.Unread {
			timeColor = config.Config.Colors.UnreadCount
		}
		header += "[" + timeColor + "::-](" + formatMessageTime(time.Unix(int64(msg.Timestamp), 0), time.Now()) + ") "
		if msg.FromMe { //msg from me
			header += "[" + colorMe + "::b]Me:[-::-]\n"
		} else { // message from others
			header += "[" + colorContact + "::b]" + nameText(msg.ContactId, msg.ContactShort) + ":[-::-]\n"
		}
	}
	out += "[\""
	out += msg.Id
	out += "\"]"
	out += header
	if msg.ReplyTo != nil {
		out += replyLine(msg.ReplyTo)
	}
	if msg.FromMe {
		text = withTicks(text, statusTicks(msg.Status), width)
	}
	out += text
	// marked so they can't be mistaken for a message that is only an emoji
	if reactions := reactionSummary(msg.Reactions); reactions != "" {
		arrow := "[gray::-] ↳"
		if hasUnreadReaction(msg, currentReceiver) {
			arrow = "[" + config.Config.Colors.UnreadCount + "::-] ↳[gray]"
		}
		out += "\n" + arrow + reactions + "[-::-]"
	}
	out += "[\"\"]"
	return out
}

// showNewMessage adds a message that arrived to the open chat, below the others
func showNewMessage(msg messages.Message) {
	// queued before another chat was opened
	if msg.ChatId != currentReceiver.Id {
		return
	}
	chatMessages = append(chatMessages, msg)
	if !messageMatches(msg, messageSearch) {
		return
	}
	// the notices stay below the messages, and the first replaces the placeholder
	if (len(notices[currentReceiver.Id]) > 0 || len(curRegions) == 0) && !printedSinceRender.Load() {
		renderMessages()
		return
	}
	var prev *messages.Message
	if endedWithReaction {
		prev = &afterReaction
	} else if len(curRegions) > 0 && messageSearch == "" {
		prev = &curRegions[len(curRegions)-1]
	}
	endedWithReaction = false
	text := getTextMessageString(&msg, prev, messageWidth())
	if strings.HasPrefix(strings.TrimPrefix(text, `["`+msg.Id+`"]`), "\n") {
		// tview shows the empty line between groups as a space before the header
		// when the text is written after the chat was drawn, but not after a space
		text = " " + text
	}
	fmt.Fprintln(textView, text)
	curRegions = append(curRegions, msg)
}

// shows the messages of the displayed chat, or the ones matching the search
func renderMessages() {
	textView.Clear()
	renderedWidth = messageWidth()
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
	screen := getMessagesString(shown, renderedWidth)
	fmt.Fprint(textView, screen)
	curRegions = append([]messages.Message(nil), shown...)
	if screen == "" && messageSearch == "" {
		if currentReceiver.Id == "" {
			PrintHelp()
		} else {
			PrintText("[::d] ~~~ no messages, press " + config.Config.Keymap.CommandBacklog + " to load backlog if available ~~~[::-]")
		}
	}
	printNotices(currentReceiver.Id)
	printedSinceRender.Store(false)
}

// printedSinceRender is whether text was printed below the messages since they
// were shown, like the QR code or the info of a message, which showing them
// again would remove. It is set from other goroutines too, e.g. for the QR code.
var printedSinceRender atomic.Bool

// nameText escapes the name of a person for the screen, in italics when it is
// only the profile name they chose, as they aren't saved in the contacts
func nameText(id, name string) string {
	if isProfileName(id) {
		return "[::i]" + tview.Escape(name) + "[::I]"
	}
	return tview.Escape(name)
}

// isProfileName returns whether a person is known by their profile name only
var isProfileName = func(id string) bool {
	return sessionManager != nil && sessionManager.IsProfileName(id)
}
