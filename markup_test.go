package main

import (
	"testing"

	"github.com/normen/whatscli/config"
)

func TestFormatMarkup(t *testing.T) {
	tests := []struct {
		text     string
		expected string
	}{
		{"*bold*", "[::b]bold[::-]"},
		{"a _italic_ word", "a [::i]italic[::-] word"},
		{"~gone~!", "[::s]gone[::-]!"},
		{"*_both_*", "[::bi]both[::-]"},
		{"*bold _and italic_*", "[::b]bold [::-][::bi]and italic[::-]"},
		{"(*bold*)", "([::b]bold[::-])"},
		// markers inside words or next to spaces don't format
		{"2*3*4 and snake_case_name", "2*3*4 and snake_case_name"},
		{"* not bold *", "* not bold *"},
		{"**", "**"},
		{"*unclosed", "*unclosed"},
		// formatting doesn't span lines
		{"*one\ntwo*", "*one\ntwo*"},
		// code is shown as it is
		{"`*not bold*`", "*not bold*"},
		{"```\n*code*\n```", "\n*code*\n"},
		// text is escaped
		{"*[red]*", "[::b][red[][::-]"},
	}
	for _, test := range tests {
		if actual := formatMarkup(test.text, "", nil); actual != test.expected {
			t.Errorf("%q: expected %q, got %q", test.text, test.expected, actual)
		}
	}
}

func TestFormatMarkupHighlightsSearch(t *testing.T) {
	if actual := formatMarkup("*big cat*", "cat", nil); actual != "[::b]big [black:yellow]cat[-:-][::-]" {
		t.Fatalf("unexpected result %q", actual)
	}
}

func TestMentionsAreHighlighted(t *testing.T) {
	color := config.Config.Colors.Mention
	actual := formatMarkup("hi @Ann Lee and *@Bob*", "", []string{"@Ann", "@Ann Lee", "@Bob"})
	expected := "hi [" + color + "]@Ann Lee[-] and [::b][" + color + "]@Bob[-][::-]"
	if actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}
