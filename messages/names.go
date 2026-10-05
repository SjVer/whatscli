package messages

import (
	"context"
	"strings"

	"github.com/nyaruka/phonenumbers"
	"go.mau.fi/whatsmeow/types"
)

// Names of people who aren't saved in the contacts: their profile name, which
// is shown in italics, see IsProfileName, or else their phone number, as it is
// written.

// unknownName is the name of someone of whom nothing is known but their LID
const unknownName = "Unknown"

// contactDisplayNames returns the name and short name to show for a contact.
// Like the phone, it prefers the name saved in the contacts over the profile
// name.
func contactDisplayNames(contact types.ContactInfo) (string, string) {
	if contact.FullName != "" || contact.FirstName != "" {
		return firstNonEmpty(contact.FullName, contact.FirstName), firstNonEmpty(contact.FirstName, contact.FullName)
	} else if contact.PushName != "" {
		return contact.PushName, contact.PushName
	}
	return contact.BusinessName, contact.BusinessName
}

// hasOnlyPushName returns whether the name of a contact is only their profile
// name, which they chose themselves, as they aren't saved in the contacts
func hasOnlyPushName(contact types.ContactInfo) bool {
	return contact.FullName == "" && contact.FirstName == "" && contact.PushName != ""
}

// IsProfileName returns whether a user is known by their profile name only,
// not by a name saved in the contacts. The phone marks them with ~.
func (sm *SessionManager) IsProfileName(id string) bool {
	return sm.db != nil && sm.db.isProfileName(id)
}

// DisplayID returns how to show a chat or user without a name: a phone number
// as it is written, like +31 6 12345678
func DisplayID(id string) string {
	jid, err := types.ParseJID(id)
	if err != nil {
		return id
	}
	switch jid.Server {
	case types.DefaultUserServer:
		return formatPhoneNumber(jid.User)
	case types.HiddenUserServer:
		return unknownName // a LID says nothing about who it is
	}
	return jid.User
}

// formatPhoneNumber writes a phone number in international format, with its
// country code and the spaces of its country
func formatPhoneNumber(number string) string {
	parsed, err := phonenumbers.Parse("+"+strings.TrimPrefix(number, "+"), "")
	if err != nil {
		return "+" + number
	}
	return phonenumbers.Format(parsed, phonenumbers.INTERNATIONAL)
}

// isFallbackName returns whether the name of a chat is only how it is shown
// without one, see DisplayID, which isn't kept as its name
func isFallbackName(chatID, name string) bool {
	user, _, _ := strings.Cut(chatID, "@")
	return name == "" || name == user || name == DisplayID(chatID)
}

// learnPushName stores the profile name of a user, which whatsmeow only does
// for the messages it receives itself, not for the ones loaded from the phone,
// and returns whether it changed.
func (sm *SessionManager) learnPushName(user types.JID, name string) bool {
	client := sm.client()
	if client == nil || client.Store.Contacts == nil || name == "" || name == "-" {
		return false
	}
	ctx := context.Background()
	user = user.ToNonAD()
	changed, _, err := client.Store.Contacts.PutPushName(ctx, user, name)
	if err != nil {
		sm.logWarn("Failed to store the profile name of %s: %v", user, err)
		return false
	} else if !changed {
		return false
	}
	// also under the phone number or LID, as whatsmeow does
	if alt, err := client.Store.GetAltJID(ctx, user); err == nil && !alt.IsEmpty() {
		if _, _, err = client.Store.Contacts.PutPushName(ctx, alt.ToNonAD(), name); err != nil {
			sm.logWarn("Failed to store the profile name of %s: %v", alt, err)
		}
	}
	sm.logDebug("Learned the profile name of %s from a message loaded from the phone", user)
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
