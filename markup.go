package main

import (
	"strings"
	"unicode"

	"github.com/normen/whatscli/config"
)

// WhatsApp text formatting: *bold*, _italic_, ~strikethrough~, and `code` or
// ```code``` which is shown as it is.

// markupAttributes maps the formatting markers to tview style attributes
var markupAttributes = map[rune]string{'*': "b", '_': "i", '~': "s"}

// markupSpan is a part of a message with the same formatting
type markupSpan struct {
	text       string
	attributes string
}

// formatMarkup escapes text for the message panel, shows its formatting and
// mentions, and highlights where it contains the search
func formatMarkup(text string, search string, mentions []string) string {
	var spans []markupSpan
	parseMarkup([]rune(text), "", &spans)
	out := ""
	current := ""
	for _, span := range spans {
		if span.attributes != current {
			if current != "" {
				out += "[::-]"
			}
			if span.attributes != "" {
				out += "[::" + span.attributes + "]"
			}
			current = span.attributes
		}
		out += highlightMentions(span.text, search, mentions)
	}
	if current != "" {
		out += "[::-]"
	}
	return out
}

// highlightMentions escapes text for the message panel, and colors the mentions
// in it, like @Alice, and where it contains the search
func highlightMentions(text, search string, mentions []string) string {
	out := ""
	for {
		// the first mention, the longest one there, so that @Ann Lee isn't taken for @Ann
		at, length := -1, 0
		for _, mention := range mentions {
			if idx := strings.Index(text, mention); idx >= 0 && (at < 0 || idx < at || idx == at && len(mention) > length) {
				at, length = idx, len(mention)
			}
		}
		if at < 0 {
			return out + highlightSearch(text, search)
		}
		out += highlightSearch(text[:at], search) +
			"[" + config.Config.Colors.Mention + "]" + highlightSearch(text[at:at+length], search) + "[-]"
		text = text[at+length:]
	}
}

// parseMarkup splits text into spans with the formatting of their markers, which
// are removed, adding to the given attributes
func parseMarkup(text []rune, attributes string, spans *[]markupSpan) {
	plain := 0
	addPlain := func(to int) {
		if to > plain {
			*spans = append(*spans, markupSpan{string(text[plain:to]), attributes})
		}
	}
	for i := 0; i < len(text); i++ {
		if text[i] == '`' && (i == 0 || isMarkupBoundary(text[i-1])) {
			marker := 1
			if strings.HasPrefix(string(text[i:min(i+3, len(text))]), "```") {
				marker = 3
			} else if !canOpenMarkup(text, i) {
				continue
			}
			if end := findCodeEnd(text, i, marker); end >= 0 {
				addPlain(i)
				*spans = append(*spans, markupSpan{string(text[i+marker : end]), attributes})
				i = end + marker - 1
				plain = i + 1
			}
			continue
		}
		attribute, ok := markupAttributes[text[i]]
		if !ok || !canOpenMarkup(text, i) || text[i+1] == text[i] {
			continue
		}
		end := findMarkupEnd(text, i)
		if end < 0 {
			continue
		}
		addPlain(i)
		inner := attributes
		if !strings.Contains(inner, attribute) {
			inner += attribute
		}
		parseMarkup(text[i+1:end], inner, spans)
		i = end
		plain = end + 1
	}
	addPlain(len(text))
}

// isMarkupBoundary returns whether a marker next to r can start or end formatting
func isMarkupBoundary(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// canOpenMarkup returns whether the marker at i can start formatting
func canOpenMarkup(text []rune, i int) bool {
	return (i == 0 || isMarkupBoundary(text[i-1])) && i+1 < len(text) && !unicode.IsSpace(text[i+1])
}

// findMarkupEnd returns where the formatting started by the marker at i ends,
// on the same line, or -1
func findMarkupEnd(text []rune, i int) int {
	for j := i + 2; j < len(text); j++ {
		if text[j] == '\n' {
			return -1
		}
		if text[j] == text[i] && !unicode.IsSpace(text[j-1]) && (j+1 == len(text) || isMarkupBoundary(text[j+1])) {
			return j
		}
	}
	return -1
}

// findCodeEnd returns where the code started by the backticks at i ends, or -1.
// Only ``` code can span several lines.
func findCodeEnd(text []rune, i int, marker int) int {
	for j := i + marker + 1; j+marker <= len(text); j++ {
		if marker == 1 && text[j] == '\n' {
			return -1
		}
		if strings.Repeat("`", marker) == string(text[j:j+marker]) &&
			(j+marker == len(text) || isMarkupBoundary(text[j+marker])) {
			return j
		}
	}
	return -1
}
