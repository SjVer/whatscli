package messages

import (
	"reflect"
	"testing"
)

func TestResolveMentions(t *testing.T) {
	members := []Member{{Id: "111@lid", Name: "Ann"}, {Id: "222@lid", Name: "Ann Lee"}, {Id: "333@s.whatsapp.net", Name: "Bob"}}
	text, mentioned := resolveMentions("hi @Ann Lee and @Bob, not Carol", members)
	if text != "hi @222 and @333, not Carol" {
		t.Errorf("unexpected text %q", text)
	}
	if !reflect.DeepEqual(mentioned, []string{"222@lid", "333@s.whatsapp.net"}) {
		t.Errorf("unexpected mentions %v", mentioned)
	}
	if text, mentioned = resolveMentions("no mentions", members); text != "no mentions" || mentioned != nil {
		t.Errorf("expected nothing to change, got %q %v", text, mentioned)
	}
}
