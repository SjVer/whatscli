package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/rivo/tview"
)

func TestEmojiQueryAt(t *testing.T) {
	withUI(t)
	tests := []struct {
		text  string
		query string
		ok    bool
	}{
		{":so", "so", true},
		{"hi :SO", "so", true},
		{"hi :thumbs_u", "thumbs_u", true},
		{"hi :s", "", false},             // too short
		{"10:30", "", false},             // a time
		{"see https://x.com", "", false}, // a link
		{"hi :sob:", "", false},          // closed
		{"hi :so ", "", false},           // done typing
		{"😭:so", "so", true},             // right after an emoji
		{"❤️:so", "so", true},            // after an emoji with a variation selector
		{"word:so", "", false},           // inside a word
	}
	for _, test := range tests {
		_, query, ok := emojiQueryAt(test.text, len(test.text))
		if ok != test.ok || query != test.query {
			t.Errorf("%q: expected %q %v, got %q %v", test.text, test.query, test.ok, query, ok)
		}
	}
	if start, _, _ := emojiQueryAt("hi :so", 6); start != 3 {
		t.Errorf("expected the colon at 3, got %d", start)
	}
}

func TestMatchEmoji(t *testing.T) {
	withUI(t)
	recentEmoji = nil
	matches := matchEmoji("sob", 8)
	if len(matches) == 0 || matches[0].name != "sob" || matches[0].emoji != "😭" {
		t.Fatalf("expected sob first, got %v", matches)
	}
	// names starting with the query come before names containing it
	matches = matchEmoji("fire", 20)
	if matches[0].name != "fire" {
		t.Fatalf("expected fire first, got %v", matches)
	}
	// each emoji is listed once
	seen := map[string]bool{}
	for _, code := range matchEmoji("thumbs", 20) {
		if seen[code.emoji] {
			t.Fatalf("expected %s to be listed once", code.emoji)
		}
		seen[code.emoji] = true
	}
	// recently used emoji come first
	recentEmoji = []string{"sweat_smile"}
	if matches = matchEmoji("s", 8); matches[0].name != "sweat_smile" {
		t.Fatalf("expected the recent emoji first, got %v", matches)
	}
	recentEmoji = nil
}

func TestReplaceShortcodes(t *testing.T) {
	withUI(t)
	recentEmoji = nil
	tests := map[string]string{
		":sob:":                    "😭",
		"so funny :joy: :joy:":     "so funny 😂 😂",
		"I :heart: you":            "I ❤️ you",
		"not :an_emoji_name: here": "not :an_emoji_name: here",
		"at 10:30:45":              "at 10:30:45",
		":joy::joy:":               "😂😂",
		"😭:joy:":                   "😭😂",
		"a:joy:":                   "a:joy:",
	}
	for text, expected := range tests {
		if actual := replaceShortcodes(text); actual != expected {
			t.Errorf("%q: expected %q, got %q", text, expected, actual)
		}
	}
	if !strings.Contains(strings.Join(recentEmoji, " "), "heart") {
		t.Errorf("expected replaced emoji to be remembered as recent, got %v", recentEmoji)
	}
	recentEmoji = nil
}

// typeText types text into the input, one key at a time
func TestEmojiAutocomplete(t *testing.T) {
	withUI(t)
	recentEmoji = nil
	textInput = newTextInput()

	typeText("so funny :so")
	if !suggestionsOpen() || suggestionPopup.suggestions[0].emoji != "sob" {
		t.Fatalf("expected suggestions starting with sob, got %v", suggestionPopup.suggestions)
	}
	second := suggestionPopup.suggestions[1]
	typeKeys(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if text := textInput.GetText(); text != "so funny "+second.insert || suggestionsOpen() {
		t.Fatalf("expected Tab and Enter to insert the second suggestion, got %q", text)
	}
	// one backspace removes it again
	typeKeys(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	if text := textInput.GetText(); text != "so funny " {
		t.Fatalf("expected one backspace to remove the emoji, got %q", text)
	}

	// the next shortcode right after an emoji shows suggestions too
	typeText(":so")
	typeKeys(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	typeText(":jo")
	if !suggestionsOpen() {
		t.Fatalf("expected suggestions right after an emoji, in %q", textInput.GetText())
	}

	// typing the whole shortcode replaces it right away
	setInput("")
	typeText("hi :sob:")
	if text := textInput.GetText(); text != "hi 😭" {
		t.Fatalf("expected :sob: to be replaced, got %q", text)
	}

	// Escape closes the suggestions until another shortcode is typed
	setInput("")
	typeText(":fir")
	typeKeys(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	typeText("e")
	if suggestionsOpen() || textInput.GetText() != ":fire" {
		t.Fatalf("expected the suggestions to stay closed, got %q", textInput.GetText())
	}
	typeText(" :fir")
	if !suggestionsOpen() {
		t.Fatal("expected suggestions for the next shortcode")
	}
	// and when it is typed again at the same place
	setInput("")
	typeText(":fir")
	typeKeys(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	setInput("")
	typeText(":fir")
	if !suggestionsOpen() {
		t.Fatal("expected suggestions for a shortcode typed again after Escape")
	}
	recentEmoji = nil
}

func TestEmojiPopupIsDrawnAboveTheInput(t *testing.T) {
	withUI(t)
	recentEmoji = nil
	screen := newScreen(t, 40, 16)
	textInput = newTextInput()
	grid := tview.NewGrid().SetRows(0, 1)
	grid.AddItem(textInput, 1, 0, 1, 1, 0, 0, true)
	grid.SetRect(0, 0, 40, 16)
	textInput.Focus(func(tview.Primitive) {})

	typeText("hi :sob")
	grid.Draw(screen)
	drawSuggestions(screen)

	found := -1
	for y := 0; y < 16; y++ {
		row := ""
		for x := 0; x < 40; x++ {
			r, _, _, _ := screen.GetContent(x, y)
			row += string(r)
		}
		if strings.Contains(row, "sob") && found < 0 {
			found = y
		}
	}
	if found < 0 || found >= 15 {
		t.Fatalf("expected the suggestions above the input, found at row %d", found)
	}
}

func TestEmojiAreColored(t *testing.T) {
	withUI(t)
	for _, code := range emojiCodes {
		if runes := []rune(code.emoji); len(runes) == 1 && runes[0] < 0x1F300 {
			t.Fatalf("%s (%s) is shown in black and white without a variation selector", code.emoji, code.name)
		}
	}
	if colorEmoji("😭") != "😭" || colorEmoji("♥") != "♥️" {
		t.Fatal("expected only black and white symbols to get a variation selector")
	}
}

func TestEmojiShortcodesCanBeDisabled(t *testing.T) {
	withUI(t)
	config.Config.General.EmojiShortcodes = false
	defer func() { config.Config.General.EmojiShortcodes = true }()

	if text := replaceShortcodes("so funny :joy:"); text != "so funny :joy:" {
		t.Fatalf("expected no replacement, got %q", text)
	}
	textInput = newTextInput()
	typeText("hi :so")
	if suggestionsOpen() {
		t.Fatal("expected no suggestions")
	}
	typeText("b:")
	if text := textInput.GetText(); text != "hi :sob:" {
		t.Fatalf("expected the shortcode to stay, got %q", text)
	}
}
