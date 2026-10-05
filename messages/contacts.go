package messages

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/types"
)

// addContactChats loads the contacts, adds a chat for every contact and returns
// how many there are.
func (sm *SessionManager) addContactChats() int {
	client := sm.client()
	if client == nil || client.Store.Contacts == nil {
		return 0
	}
	contacts, err := client.Store.Contacts.GetAllContacts(context.Background())
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to load contacts: %v", err))
		return 0
	}
	addedChats := 0
	for jid, contact := range contacts {
		// without a name, the number is shown, see DisplayID
		name, short := contactDisplayNames(contact)
		sm.db.AddContact(Contact{Id: jid.String(), Name: name, Short: short, ProfileName: hasOnlyPushName(contact)})
		if jid.Server == types.DefaultUserServer {
			sm.db.AddChat(Chat{Id: jid.String(), Name: name})
			addedChats++
		}
	}
	sm.nameOwnChat(client.Store.PushName)
	return addedChats
}

// nameOwnChat names the chat with yourself like the phone does, once your name is known.
func (sm *SessionManager) nameOwnChat(pushName string) {
	if own := sm.client().Store.GetJID(); !own.IsEmpty() && pushName != "" {
		sm.db.AddChat(Chat{Id: own.ToNonAD().String(), Name: pushName + " (You)"})
	}
}

// learnLID stores that the users lid and pn are the same, and merges a chat that
// was stored under the LID. The phone sends these with the chat history, whatsmeow
// stores them too but in the background, so they may be missing when needed.
func (sm *SessionManager) learnLID(lid, pn string) {
	client := sm.client()
	lidJID, lidErr := types.ParseJID(lid)
	pnJID, pnErr := types.ParseJID(pn)
	if lidErr != nil || pnErr != nil || lidJID.Server != types.HiddenUserServer || pnJID.Server != types.DefaultUserServer || client == nil {
		return
	}
	lidJID, pnJID = lidJID.ToNonAD(), pnJID.ToNonAD()
	if err := client.Store.LIDs.PutLIDMapping(context.Background(), lidJID, pnJID); err != nil {
		return
	}
	sm.db.MergeChat(lidJID.String(), pnJID.String())
}

// mergeLIDChats merges chats that were stored under a LID before its phone number was known.
func (sm *SessionManager) mergeLIDChats() {
	for _, chat := range sm.db.GetChatIds() {
		jid, err := types.ParseJID(chat.Id)
		if err != nil || jid.Server != types.HiddenUserServer {
			continue
		}
		if chatID := sm.chatIdForJID(jid); chatID != chat.Id {
			sm.db.MergeChat(chat.Id, chatID)
		}
	}
}

// chatIdForJID returns the chat id for jid, mapping hidden user ids (LIDs) to phone numbers.
func (sm *SessionManager) chatIdForJID(jid types.JID) string {
	client := sm.client()
	if jid.Server == types.HiddenUserServer && client != nil && client.Store.LIDs != nil {
		if pn, err := client.Store.LIDs.GetPNForLID(context.Background(), jid); err == nil && !pn.IsEmpty() {
			return pn.ToNonAD().String()
		}
	}
	return jid.ToNonAD().String()
}

// getChatName returns the name of a group or user.
func (sm *SessionManager) getChatName(jid types.JID) string {
	if jid.Server == types.GroupServer {
		if client, err := sm.connectedClient(); err == nil {
			if groupInfo, err := client.GetGroupInfo(context.Background(), jid); err == nil && groupInfo.Name != "" {
				return groupInfo.Name
			}
		}
		return sm.db.GetIdName(jid.String())
	}
	_, name, _ := sm.contactNames(jid)
	return name
}

// contactNames returns the id, name and short name to show for a user. Like the
// phone, it prefers the name saved in the contacts over the user's profile name.
func (sm *SessionManager) contactNames(jid types.JID) (string, string, string) {
	id, name, short, ok := sm.savedNames(jid)
	if !ok {
		return id, sm.db.GetIdName(id), sm.db.GetIdShort(id)
	}
	return id, name, short
}

// realName returns the name of a user, and whether it is one, not only how
// they are shown without one, see isFallbackName
func (sm *SessionManager) realName(jid types.JID) (string, bool) {
	id, name, _ := sm.contactNames(jid)
	return name, !isFallbackName(id, name)
}

// savedNames returns the ID of a user, by phone number when it is known, and
// their name and short name from whatsmeow's contacts, if it has one
func (sm *SessionManager) savedNames(jid types.JID) (string, string, string, bool) {
	client := sm.client()
	jid = jid.ToNonAD()
	// contacts are stored under the phone number, groups address users by LID,
	// and profile names can be known under either
	jids := []types.JID{jid}
	if pn, err := types.ParseJID(sm.chatIdForJID(jid)); err == nil && pn != jid {
		jids = []types.JID{pn, jid}
	} else if lid, err := sm.phoneChatJID(jid.String()); err == nil && lid != jid {
		jids = []types.JID{jid, lid}
	}
	id := jids[0].String()
	if client != nil && client.Store.Contacts != nil {
		for _, lookup := range jids {
			contact, err := client.Store.Contacts.GetContact(context.Background(), lookup)
			if err != nil || !contact.Found {
				continue
			}
			if name, short := contactDisplayNames(contact); name != "" {
				return id, name, short, true
			}
		}
	}
	return id, "", "", false
}

// refreshContactNames updates the sender names of the messages in memory, which
// may have been saved or received before the contacts were known.
func (sm *SessionManager) refreshContactNames() {
	sm.db.UpdateContactNames(func(contactID string) (string, string, string, bool) {
		jid, err := types.ParseJID(contactID)
		if err != nil {
			return "", "", "", false
		}
		id, name, short := sm.contactNames(jid)
		return id, name, short, true
	})
	// the names of mentioned people and of who sent what a message replies to,
	// also in messages saved with an older name or number, or before replies
	// were shown
	sm.db.UpdateMessageContents(func(msg Message) (MessageContent, bool) {
		ctx := contextInfo(msg.RawMessage)
		if ctx.GetStanzaID() == "" && len(ctx.GetMentionedJID()) == 0 {
			return MessageContent{}, false
		}
		content := msg.content()
		if ext := msg.RawMessage.GetExtendedTextMessage(); len(ctx.GetMentionedJID()) > 0 && ext != nil {
			content.Text, content.Mentions = sm.showMentions(ext.GetText(), ctx.GetMentionedJID())
		}
		content.ReplyTo = sm.eventHandler.replyOf(messageInfo(msg), ctx)
		return content, true
	})
}
