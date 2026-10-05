package messages

import (
	"sort"
	"strings"

	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// Status of the user's messages: sent when WhatsApp got it, delivered when it
// arrived on the phone of who it was sent to, and read when they read it. In
// groups, that is when it arrived for, or was read by, every member, like the
// phone shows it, and the receipts of each member are kept for the info.

// MessageStatus is how far a message of the user got
type MessageStatus int

const (
	StatusUnknown MessageStatus = iota
	StatusSent
	StatusDelivered
	StatusRead
)

func (status MessageStatus) String() string {
	switch status {
	case StatusSent:
		return "sent"
	case StatusDelivered:
		return "delivered"
	case StatusRead:
		return "read"
	}
	return "unknown"
}

// userKey returns how a user is known in Message.Receipts and of the members
// of a group: by phone number when it is known, like chatIdForJID
func (sm *SessionManager) userKey(jid types.JID) string {
	return sm.chatIdForJID(jid)
}

// receiptStatus returns the status a receipt for a message tells, or
// StatusUnknown for receipts that don't tell one, like the ones of the user's
// own devices
func receiptStatus(receiptType types.ReceiptType) MessageStatus {
	switch receiptType {
	case types.ReceiptTypeDelivered:
		return StatusDelivered
	case types.ReceiptTypeRead, types.ReceiptTypePlayed:
		return StatusRead
	}
	return StatusUnknown
}

// historyStatus returns the status of a message of the user loaded from the phone
func historyStatus(status waWeb.WebMessageInfo_Status) MessageStatus {
	switch status {
	case waWeb.WebMessageInfo_SERVER_ACK:
		return StatusSent
	case waWeb.WebMessageInfo_DELIVERY_ACK:
		return StatusDelivered
	case waWeb.WebMessageInfo_READ, waWeb.WebMessageInfo_PLAYED:
		return StatusRead
	}
	return StatusUnknown
}

// handleStatusReceipt records how far the user's messages got, from a receipt of
// who they were sent to.
func (eh *eventHandler) handleStatusReceipt(evt *events.Receipt) {
	status := receiptStatus(evt.Type)
	if status == StatusUnknown {
		return
	}
	chatID := eh.sm.chatIdForJID(evt.Chat)
	member := eh.sm.userKey(evt.Sender)
	// until they are loaded, see loadGroupMembers
	_, members := eh.sm.loadedMembers(chatID)
	changed := false
	for _, id := range evt.MessageIDs {
		updated, ok := eh.sm.db.SetReceipt(id, member, status, func(receipts map[string]MessageStatus) MessageStatus {
			return groupStatus(receipts, members)
		})
		if !ok {
			eh.sm.logDebug("Receipt %s of %s for message %s, which isn't loaded or not the user's", status, member, id)
		} else if updated != StatusUnknown {
			eh.sm.logDebug("Message %s in %s is %s now", id, chatID, updated)
			changed = true
		}
	}
	if changed && eh.sm.offlineMessages.Load() == 0 {
		eh.sm.showIfOpen(chatID)
	}
}

// groupStatus returns the status of a message in a group from the receipts of
// its members: the least far it got for any of them. While the members aren't
// known, it can only be told that it arrived.
func groupStatus(receipts map[string]MessageStatus, members []string) MessageStatus {
	if len(members) == 0 {
		return StatusDelivered
	}
	status := StatusRead
	for _, member := range members {
		status = min(status, max(receipts[member], StatusSent))
	}
	return status
}

// receiptInfo returns who received and read a message of the user in a group, for the info
func (sm *SessionManager) receiptInfo(messageID string) string {
	msg, ok := sm.db.GetMessage(messageID)
	if !ok || len(msg.Receipts) == 0 {
		return ""
	}
	var read, delivered []string
	for member, status := range msg.Receipts {
		name, _ := sm.userNames(member)
		if status == StatusRead {
			read = append(read, name)
		} else {
			delivered = append(delivered, name)
		}
	}
	sort.Strings(read)
	sort.Strings(delivered)
	out := ""
	if len(read) > 0 {
		out += "\nRead by: " + strings.Join(read, ", ")
	}
	if len(delivered) > 0 {
		out += "\nDelivered to: " + strings.Join(delivered, ", ")
	}
	return out
}
