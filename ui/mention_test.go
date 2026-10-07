package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

func TestMentionQueryAt(t *testing.T) {
	withUI(t)
	cases := []struct {
		text  string
		query string
		ok    bool
	}{
		{"@", "", true},
		{"hi @Al", "al", true},
		{"@bo", "bo", true},
		{"mail me@example", "", false},
		{"hi @al ", "", false},
	}
	for _, c := range cases {
		_, query, ok := mentionQueryAt(c.text, len(c.text))
		if ok != c.ok || query != c.query {
			t.Errorf("%q: expected %q %v, got %q %v", c.text, c.query, c.ok, query, ok)
		}
	}
}

func TestMatchMembers(t *testing.T) {
	withUI(t)
	members := []messages.Member{{Id: "1@lid", Name: "Alice Smith"}, {Id: "2@lid", Name: "Bob"}, {Id: "3@lid", Name: "Carol Lee"}}
	matches := matchMembers(members, "l", 8)
	// names with a word starting with l first
	if len(matches) != 2 || matches[0].Name != "Carol Lee" || matches[1].Name != "Alice Smith" {
		t.Errorf("unexpected matches %v", matches)
	}
	if all := matchMembers(members, "", 2); len(all) != 2 {
		t.Errorf("expected the limit to apply, got %v", all)
	}
}

func TestMentionsAreHighlightedInTheInput(t *testing.T) {
	withUI(t)
	defer func(members func() []messages.Member, input *tview.TextArea) {
		groupMembers, textInput = members, input
	}(groupMembers, textInput)
	groupMembers = func() []messages.Member {
		return []messages.Member{{Id: "1@lid", Name: "Ann"}, {Id: "2@lid", Name: "Ann Lee"}, {Id: "3@lid", Name: "Bob"}}
	}
	textInput = newTextInput()
	textInput.SetText("hi @Ann Lee and Bob", false)
	textInput.SetRect(0, 0, 30, 1)
	screen := tcell.NewSimulationScreen("")
	screen.Init()
	screen.SetSize(30, 1)
	textInput.Draw(screen)
	highlightInputMentions(screen)

	mention := tcell.ColorNames[config.Config.Colors.Mention]
	colors := ""
	for x := 0; x < 19; x++ {
		if _, _, style, _ := screen.GetContent(x, 0); colorOf(style) == mention {
			colors += "m"
		} else {
			colors += "."
		}
	}
	// "hi @Ann Lee and Bob": the whole longer name, and not Bob without an @
	if colors != "...mmmmmmmm........" {
		t.Errorf("expected only the mention to be colored, got %q", colors)
	}
}

func colorOf(style tcell.Style) tcell.Color {
	fg, _, _ := style.Decompose()
	return fg
}
