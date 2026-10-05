package main

import (
	_ "embed"
	"regexp"
	"strings"

	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

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

// messageMatches returns whether the text of a message, with its alt text, contains the search, ignoring case
func messageMatches(msg messages.Message, search string) bool {
	return search == "" || strings.Contains(strings.ToLower(messages.WithAltText(msg.Text, msg.AltText)), strings.ToLower(search))
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
