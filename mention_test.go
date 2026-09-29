package main

import (
	"testing"

	"github.com/normen/whatscli/messages"
)

func TestMentionQueryAt(t *testing.T) {
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
