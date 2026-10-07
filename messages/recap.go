package messages

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/normen/whatscli/ai"
	"github.com/normen/whatscli/config"
)

// Recaps and questions: /recap asks the AI model, see ensureModel, to sum up
// the messages of the open chat of a time, and /ask to answer a question about
// them. The answer is shown in a notice below the messages.

// the keys of the notices of the open chat that show the recap and the answer
const (
	recapNotice = "recap"
	askNotice   = "ask"
)

// recapCount is how many messages are recapped when no time is given and none are unread
const recapCount = 50

// the longest transcript sent to the model, so that it fits its context with
// the answer, see ai.Server; the oldest messages are left out of longer ones
const maxTranscript = 16000

// maxAnswerTokens is how long a recap or answer may be
const maxAnswerTokens = 400

// recap sums up the messages of the open chat of the time in params, like
// 10m, or the unread ones, or the last recapCount if none are
func (sm *SessionManager) recap(params []string) {
	chatID := sm.chatForModel("recap", "[time[]")
	if chatID == "" {
		return
	}
	since, err := parseDuration(strings.Join(params, " "))
	if err != nil && len(params) > 0 {
		sm.uiHandler.PrintError(err)
		return
	}
	msgs := sm.db.GetMessages(chatID)
	if since > 0 {
		msgs = messagesSince(msgs, time.Now().Add(-since))
	} else {
		msgs = unreadOrLast(msgs, recapCount)
	}
	prompt := "Sum up these messages for someone who didn't read them, in a few short sentences, naming who said what, without a preamble."
	sm.askAboutChat(chatID, recapNotice, msgs, prompt, func(answer string) string {
		return fmt.Sprintf("Recap of %d messages: %s", len(msgs), answer)
	})
}

// ask answers a question about the messages of the open chat, like
// "5d what did we plan for sunday?", from those of the time it starts with,
// or from all the loaded ones without a time
func (sm *SessionManager) ask(params []string) {
	chatID := sm.chatForModel("ask", "[time[] [question[]")
	if chatID == "" {
		return
	}
	since, question := splitTime(params)
	if question == "" {
		sm.printCommandUsage("ask", "[time[] [question[]")
		return
	}
	msgs := sm.db.GetMessages(chatID)
	if since > 0 {
		msgs = messagesSince(msgs, time.Now().Add(-since))
	}
	prompt := "Answer this question from these messages only, in a few short sentences, without a preamble. " +
		"If they don't answer it, say so.\nQuestion: " + question
	sm.askAboutChat(chatID, askNotice, msgs, prompt, func(answer string) string {
		return "Q: " + question + "\nA: " + answer
	})
}

// chatForModel returns the open chat for a command that asks the model about
// it, or "" after telling why it can't
func (sm *SessionManager) chatForModel(command, usage string) string {
	chatID := sm.openChat()
	if chatID == "" {
		sm.printCommandUsage(command, usage+" -> only works in a chat")
	} else if config.Config.General.AiModel == "" {
		sm.uiHandler.PrintError(errors.New("set ai_model in the config for " + config.Config.General.CmdPrefix + command + ", see the README"))
		return ""
	}
	return chatID
}

// askAboutChat asks the model what the task says to do with msgs of a chat,
// and shows its answer, as show writes it, in the notice with the key
func (sm *SessionManager) askAboutChat(chatID, key string, msgs []Message, task string, show func(answer string) string) {
	if len(msgs) == 0 {
		sm.uiHandler.SetNotice(chatID, key, "No messages in that time")
		return
	}
	sm.uiHandler.SetNotice(chatID, key, fmt.Sprintf("Asking the AI model about %d messages...", len(msgs)))
	url, err := sm.ensureModel()
	if err != nil {
		sm.uiHandler.SetNotice(chatID, key, "")
		return // the notice of the model tells why
	}
	answer, err := ai.Ask(url, chatPrompt(time.Now(), msgs, task), maxAnswerTokens, "")
	if answer = withoutPreamble(answer); err == nil && answer == "" {
		err = errors.New("the model gave an empty answer")
	}
	if err != nil {
		sm.uiHandler.SetNotice(chatID, key, "The AI model failed: "+err.Error())
		return
	}
	sm.uiHandler.SetNotice(chatID, key, show(answer))
}

// chatPrompt gives the model msgs and then the task, with the date, so that
// it can tell which day "sunday" is
func chatPrompt(now time.Time, msgs []Message, task string) string {
	return "Today is " + now.Format("Monday 2 January 2006") + ". Here are WhatsApp messages, each with when it was sent and who sent it.\n\n" +
		transcript(msgs) + "\n\n" + task
}

// messagesSince returns the messages, sorted by time, that were sent from then on
func messagesSince(msgs []Message, from time.Time) []Message {
	for idx, msg := range msgs {
		if msg.Timestamp >= uint64(from.Unix()) {
			return msgs[idx:]
		}
	}
	return nil
}

// unreadOrLast returns the unread messages, sorted by time, or the last count if none are
func unreadOrLast(msgs []Message, count int) []Message {
	for idx, msg := range msgs {
		if msg.Unread {
			return msgs[idx:]
		}
	}
	return msgs[max(0, len(msgs)-count):]
}

// transcript writes messages as text for the model, a line each with its time
// and sender, at most maxTranscript long, without the oldest if it would be longer
func transcript(msgs []Message) string {
	start, length := len(msgs), 0
	for start > 0 {
		length += len(transcriptLine(msgs[start-1])) + 1
		if length > maxTranscript && start < len(msgs) {
			break
		}
		start--
	}
	lines := make([]string, 0, len(msgs)-start)
	for _, msg := range msgs[start:] {
		lines = append(lines, transcriptLine(msg))
	}
	return strings.Join(lines, "\n")
}

// transcriptLine writes a message on one line, see transcript
func transcriptLine(msg Message) string {
	name := msg.ContactShort
	if msg.FromMe {
		name = "Me"
	} else if name == "" {
		name = msg.ContactName
	}
	text := strings.ReplaceAll(WithAltText(msg.Text, msg.AltText), "\n", " ")
	return time.Unix(int64(msg.Timestamp), 0).Format("Mon 2 Jan 15:04") + " " + name + ": " + text
}

// withoutPreamble leaves out the line models start with although they were
// asked not to, like "Here's a summary of the conversation:", and empty lines
func withoutPreamble(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > 1 && strings.HasSuffix(lines[0], ":") {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

var durationPart = regexp.MustCompile(`^(\d+(?:\.\d+)?)([a-z]+)`)

// durationUnits are the units of parseDuration, by the names that are written for them
var durationUnits = map[string]time.Duration{}

func init() {
	for unit, names := range map[time.Duration][]string{
		time.Second:        {"s", "sec", "secs", "second", "seconds"},
		time.Minute:        {"m", "min", "mins", "minute", "minutes"},
		time.Hour:          {"h", "hr", "hrs", "hour", "hours"},
		24 * time.Hour:     {"d", "day", "days"},
		7 * 24 * time.Hour: {"w", "wk", "wks", "week", "weeks"},
	} {
		for _, name := range names {
			durationUnits[name] = unit
		}
	}
}

// parseDuration reads a time the way people write it, like 10m, 1h30m, 2 days
// or 1.5 hours
func parseDuration(text string) (time.Duration, error) {
	rest := strings.ReplaceAll(strings.ToLower(text), " ", "")
	if rest == "" {
		return 0, errors.New("no time given")
	}
	var total time.Duration
	for rest != "" {
		match := durationPart.FindStringSubmatch(rest)
		if match == nil || durationUnits[match[2]] == 0 {
			return 0, fmt.Errorf("%q isn't a time like 10m, 2h or 3 days", text)
		}
		amount, _ := strconv.ParseFloat(match[1], 64)
		total += time.Duration(amount * float64(durationUnits[match[2]]))
		rest = rest[len(match[0]):]
	}
	return total, nil
}

// splitTime splits the params of /ask into the time they start with, which
// can be several words like "3 days", 0 if there is none, and the rest
func splitTime(params []string) (time.Duration, string) {
	for words := min(len(params), 4); words > 0; words-- {
		if since, err := parseDuration(strings.Join(params[:words], " ")); err == nil {
			return since, strings.Join(params[words:], " ")
		}
	}
	return 0, strings.Join(params, " ")
}
