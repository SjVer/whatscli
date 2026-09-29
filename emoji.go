package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/kyokomi/emoji/v2"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
)

// Emoji shortcodes like Discord and Slack have them: typing :sob: gives 😭, and
// typing :so shows suggestions, see suggestionPopup.

// emojiCode is an emoji with one of its shortcode names, without the colons
type emojiCode struct {
	name  string
	emoji string
}

// emojiCodes are all known shortcodes, sorted by name
var emojiCodes = loadEmojiCodes()

// emojiByName maps shortcode names to their emoji
var emojiByName = map[string]string{}

func loadEmojiCodes() []emojiCode {
	var codes []emojiCode
	for code, value := range emoji.CodeMap() {
		name := strings.Trim(code, ":")
		codes = append(codes, emojiCode{name, colorEmoji(strings.TrimSpace(value))})
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i].name < codes[j].name })
	for _, code := range codes {
		emojiByName[code.name] = code.emoji
	}
	return codes
}

// colorEmoji adds the variation selector that asks for the colored emoji to
// symbols that are shown in black and white without it, like ☺ and ♥. It
// changes nothing for emoji that are colored anyway.
func colorEmoji(value string) string {
	if runes := []rune(value); len(runes) == 1 && runes[0] >= 0x80 && runes[0] < 0x1F300 {
		return value + "️"
	}
	return value
}

// maxRecentEmoji is how many recently used emoji are remembered
const maxRecentEmoji = 30

// recentEmoji are the names of the emoji used last, the most recent first
var recentEmoji []string

// isEmojiNameChar returns whether r can be part of a shortcode name
func isEmojiNameChar(r rune) bool {
	return r == '_' || r == '-' || r == '+' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// canStartShortcode returns whether a shortcode can start after r: at the start
// of a word or right after an emoji, which may end with a variation selector or
// keycap, but not after letters and digits, so times like 10:30 and links don't.
func canStartShortcode(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsSymbol(r) || r == '️' || r == '⃣'
}

// emojiQueryAt returns the shortcode being typed before the cursor, like "so"
// in "hi :so", and where its colon is, see canStartShortcode.
func emojiQueryAt(text string, cursor int) (int, string, bool) {
	start := cursor
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:start])
		if !isEmojiNameChar(r) {
			break
		}
		start -= size
	}
	if start == 0 || text[start-1] != ':' || cursor-start < 2 {
		return 0, "", false
	}
	colon := start - 1
	if colon > 0 {
		if r, _ := utf8.DecodeLastRuneInString(text[:colon]); !canStartShortcode(r) {
			return 0, "", false
		}
	}
	return colon, strings.ToLower(text[start:cursor]), true
}

// matchEmoji returns up to limit emoji whose name contains query: recently used
// ones first, then names starting with it, then the others. Each emoji is
// listed once, under its best matching name.
func matchEmoji(query string, limit int) []emojiCode {
	recentRank := make(map[string]int, len(recentEmoji))
	for idx, name := range recentEmoji {
		recentRank[name] = idx
	}
	var matches []emojiCode
	for _, code := range emojiCodes {
		if strings.Contains(code.name, query) {
			matches = append(matches, code)
		}
	}
	rank := func(code emojiCode) (int, int) {
		if idx, ok := recentRank[code.name]; ok {
			return 0, idx
		}
		if strings.HasPrefix(code.name, query) {
			return 1, len(code.name)
		}
		return 2, len(code.name)
	}
	sort.SliceStable(matches, func(i, j int) bool {
		groupI, orderI := rank(matches[i])
		groupJ, orderJ := rank(matches[j])
		if groupI != groupJ {
			return groupI < groupJ
		}
		return orderI < orderJ
	})
	seen := make(map[string]bool)
	var result []emojiCode
	for _, code := range matches {
		if !seen[code.emoji] && len(result) < limit {
			seen[code.emoji] = true
			result = append(result, code)
		}
	}
	return result
}

// shortcodePattern matches a shortcode like :sob:, see canStartShortcode
var shortcodePattern = regexp.MustCompile(`(^|[\s\p{S}\x{FE0F}\x{20E3}]):([\p{L}\p{N}_+\-]+):`)

// replaceShortcodes replaces the known shortcodes in text with their emoji,
// and remembers them as recently used
func replaceShortcodes(text string) string {
	if !config.Config.General.EmojiShortcodes {
		return text
	}
	// again until nothing changes, as :joy::joy: only has the second one after
	// an emoji once the first one was replaced
	for {
		replaced := shortcodePattern.ReplaceAllStringFunc(text, func(match string) string {
			prefix, name, _ := strings.Cut(match, ":")
			name = strings.TrimSuffix(name, ":")
			value, ok := emojiByName[strings.ToLower(name)]
			if !ok {
				return match
			}
			useEmoji(strings.ToLower(name))
			return prefix + value
		})
		if replaced == text {
			return text
		}
		text = replaced
	}
}

// useEmoji moves an emoji to the front of the recently used ones, and saves them
func useEmoji(name string) {
	recent := []string{name}
	for _, other := range recentEmoji {
		if other != name && len(recent) < maxRecentEmoji {
			recent = append(recent, other)
		}
	}
	recentEmoji = recent
	saveRecentEmoji()
}

// recentEmojiPath is where the recently used emoji are saved, next to the
// config, or "" when there is no config, e.g. in tests
func recentEmojiPath() string {
	if config.GetConfigFilePath() == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(config.GetConfigFilePath()), "recent_emoji.json")
}

func loadRecentEmoji() {
	if path := recentEmojiPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			json.Unmarshal(data, &recentEmoji)
		}
	}
}

func saveRecentEmoji() {
	if path := recentEmojiPath(); path != "" {
		if data, err := json.Marshal(recentEmoji); err == nil {
			os.WriteFile(path, data, 0600)
		}
	}
}

// maxSuggestions is how many suggestions the popup shows
const maxSuggestions = 8

// suggestion is an emoji or a group member suggested in the popup
type suggestion struct {
	// as it is shown in the popup
	label string
	// what replaces the typed shortcode or mention
	insert string
	// the name of the emoji, which is remembered as recently used, "" for a member
	emoji string
}

// suggestionPopup shows suggestions while a shortcode is typed in the input, or
// the members of a group while a mention is, see mentionQueryAt
var suggestionPopup struct {
	suggestions []suggestion
	// whether the suggestions are members to mention
	mentions bool
	selected int
	// where the colon of the shortcode is in the input, and what was typed after it
	start int
	query string
	// the shortcode whose suggestions were closed with Escape, by its colon
	dismissed int
}

// suggestionsOpen returns whether suggestions are shown
func suggestionsOpen() bool {
	return len(suggestionPopup.suggestions) > 0
}

func closeSuggestions() {
	suggestionPopup.suggestions = nil
}

// updateSuggestions shows suggestions for the shortcode or mention typed
// before the cursor, or replaces a shortcode with its emoji when its closing
// colon was typed
func updateSuggestions() {
	text := textInput.GetText()
	selected, cursor, _ := textInput.GetSelection()
	if selected != "" {
		closeSuggestions()
		return
	}
	shortcodes := config.Config.General.EmojiShortcodes
	if shortcodes && cursor > 0 && text[cursor-1] == ':' {
		if start, query, ok := emojiQueryAt(text, cursor-1); ok {
			if value, known := emojiByName[query]; known {
				useEmoji(query)
				closeSuggestions()
				textInput.Replace(start, cursor, value)
				return
			}
		}
	}
	start, query, ok := emojiQueryAt(text, cursor)
	mentions := false
	if !ok || !shortcodes {
		start, query, ok = mentionQueryAt(text, cursor)
		mentions = true
	}
	if !ok {
		// a shortcode typed at the same place again is suggested again
		suggestionPopup.dismissed = -1
		closeSuggestions()
		return
	} else if start == suggestionPopup.dismissed {
		closeSuggestions()
		return
	}
	if start != suggestionPopup.start || query != suggestionPopup.query || mentions != suggestionPopup.mentions {
		suggestionPopup.selected = 0
	}
	suggestionPopup.start, suggestionPopup.query, suggestionPopup.mentions = start, query, mentions
	suggestionPopup.suggestions = nil
	if mentions {
		for _, member := range matchMembers(groupMembers(), query, maxSuggestions) {
			suggestionPopup.suggestions = append(suggestionPopup.suggestions, suggestion{label: "@" + member.Name, insert: "@" + member.Name + " "})
		}
	} else {
		for _, code := range matchEmoji(query, maxSuggestions) {
			suggestionPopup.suggestions = append(suggestionPopup.suggestions, suggestion{label: code.emoji + "  " + code.name, insert: code.emoji, emoji: code.name})
		}
	}
	suggestionPopup.selected = min(suggestionPopup.selected, max(0, len(suggestionPopup.suggestions)-1))
}

// cycleSuggestion selects the next suggestion, or the previous one with a negative step
func cycleSuggestion(step int) {
	count := len(suggestionPopup.suggestions)
	suggestionPopup.selected = ((suggestionPopup.selected+step)%count + count) % count
}

// acceptSuggestion replaces the typed shortcode or mention with the selected suggestion
func acceptSuggestion() {
	selected := suggestionPopup.suggestions[suggestionPopup.selected]
	_, cursor, _ := textInput.GetSelection()
	closeSuggestions()
	if selected.emoji != "" {
		useEmoji(selected.emoji)
	}
	textInput.Replace(suggestionPopup.start, cursor, selected.insert)
}

// handleSuggestionKeys lets the popup handle a key while it is open: Tab, Down
// and Up select a suggestion, Enter accepts it and Escape closes the popup
func handleSuggestionKeys(event *tcell.EventKey) bool {
	if !suggestionsOpen() {
		return false
	}
	switch event.Key() {
	case tcell.KeyTab, tcell.KeyDown:
		cycleSuggestion(1)
	case tcell.KeyBacktab, tcell.KeyUp:
		cycleSuggestion(-1)
	case tcell.KeyEnter:
		acceptSuggestion()
	case tcell.KeyEscape:
		suggestionPopup.dismissed = suggestionPopup.start
		closeSuggestions()
	default:
		return false
	}
	return true
}

// drawSuggestions draws the suggestions above the input, starting at the colon
func drawSuggestions(screen tcell.Screen) {
	if !suggestionsOpen() || !textInput.HasFocus() {
		return
	}
	list := tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(true)
	list.SetBorder(true)
	width := 0
	for _, suggested := range suggestionPopup.suggestions {
		list.AddItem(suggested.label, "", 0, nil)
		width = max(width, uniseg.StringWidth(suggested.label))
	}
	list.SetCurrentItem(suggestionPopup.selected)

	inputX, inputY, _, _ := textInput.GetInnerRect()
	_, _, cursorRow, cursorColumn := textInput.GetCursor()
	offsetRow, offsetColumn := textInput.GetOffset()
	screenWidth, _ := screen.Size()
	width += 4 // borders and padding
	height := len(suggestionPopup.suggestions) + 2
	x := inputX + cursorColumn - offsetColumn - uniseg.StringWidth(suggestionPopup.query) - 2
	x = max(0, min(x, screenWidth-width))
	y := max(0, inputY+cursorRow-offsetRow-height)
	list.SetRect(x, y, width, height)
	list.Draw(screen)
}

// mentionQueryAt returns the name being typed after @ before the cursor, like
// "al" in "hi @al", and where its @ is. It starts at the start of a word, so
// that e-mail addresses don't.
func mentionQueryAt(text string, cursor int) (int, string, bool) {
	start := cursor
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:start])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		start -= size
	}
	if start == 0 || text[start-1] != '@' {
		return 0, "", false
	}
	at := start - 1
	if at > 0 {
		if r, _ := utf8.DecodeLastRuneInString(text[:at]); !unicode.IsSpace(r) {
			return 0, "", false
		}
	}
	return at, strings.ToLower(text[start:cursor]), true
}

// groupMembers returns the members of the open chat that can be mentioned,
// none if it isn't a group
func groupMembers() []messages.Member {
	if sessionManager == nil {
		return nil
	}
	return sessionManager.GroupMembers(currentReceiver.Id)
}

// matchMembers returns up to limit members whose name contains query, the ones
// with a word of their name starting with it first
func matchMembers(members []messages.Member, query string, limit int) []messages.Member {
	var first, other []messages.Member
	for _, member := range members {
		name := strings.ToLower(member.Name)
		if !strings.Contains(name, query) {
			continue
		}
		if strings.HasPrefix(name, query) || strings.Contains(name, " "+query) {
			first = append(first, member)
		} else {
			other = append(other, member)
		}
	}
	matches := append(first, other...)
	return matches[:min(len(matches), limit)]
}
