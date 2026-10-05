package messages

import (
	"sort"
	"strings"

	"github.com/rivo/tview"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/types"
)

// reactionInfo lists who reacted to a message with what, for the message info
func (sm *SessionManager) reactionInfo(messageID string) string {
	msg, ok := sm.db.GetMessage(messageID)
	if !ok || len(msg.Reactions) == 0 {
		return ""
	}
	reactions := sm.namedReactions(msg)
	for idx, reaction := range reactions {
		reactions[idx] = "  " + tview.Escape(reaction)
	}
	return "\nReactions:\n" + strings.Join(reactions, "\n")
}

// namedReactions returns the reactions to a message as the emoji and the name
// of who reacted, sorted
func (sm *SessionManager) namedReactions(msg Message) []string {
	reactions := make([]string, 0, len(msg.Reactions))
	for reactor, reaction := range msg.Reactions {
		name, _ := sm.userNames(reactor)
		reactions = append(reactions, reaction+" "+name)
	}
	sort.Strings(reactions)
	return reactions
}

// ShortName returns the short name to show for a user, "You" for yourself.
func (sm *SessionManager) ShortName(id string) string {
	_, short := sm.userNames(id)
	return short
}

// userNames returns the name and short name of a user by ID, like a reactor in
// Message.Reactions or a member in Message.Receipts, "You" for yourself.
func (sm *SessionManager) userNames(reactor string) (string, string) {
	if reactor == ReactorMe {
		return "You", "You"
	}
	if jid, err := types.ParseJID(reactor); err == nil {
		_, name, short := sm.contactNames(jid)
		return name, short
	}
	return reactor, reactor
}

// reactorID returns the key of a reaction in Message.Reactions
func (sm *SessionManager) reactorID(sender types.JID, fromMe bool) string {
	if fromMe {
		return ReactorMe
	}
	return sm.chatIdForJID(sender)
}

// reactorFromKey returns who reacted from the key of a reaction in the chat history
func (sm *SessionManager) reactorFromKey(key *waCommon.MessageKey, chat types.JID) string {
	if key.GetFromMe() {
		return ReactorMe
	}
	if participant, err := types.ParseJID(key.GetParticipant()); err == nil && !participant.IsEmpty() {
		return sm.chatIdForJID(participant)
	}
	return sm.chatIdForJID(chat)
}
