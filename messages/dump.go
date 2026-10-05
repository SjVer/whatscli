package messages

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
)

// Dump writes the chat list as whatscli shows it, with the data it is based on,
// and all unread messages. Used by the -dump command line option for debugging.
func (sm *SessionManager) Dump(w io.Writer) {
	client := sm.client()
	var shown, archived, hidden []Chat
	for _, chat := range sm.db.GetChatIds() {
		if chat.Hidden {
			hidden = append(hidden, chat)
		} else if chat.InArchive {
			archived = append(archived, chat)
		} else {
			shown = append(shown, chat)
		}
	}

	connected := client != nil && client.IsConnected()
	fmt.Fprintf(w, "connected: %v\n", connected)
	if lastReceived := sm.LastReceived(); !lastReceived.IsZero() {
		fmt.Fprintf(w, "last data received: %s ago\n", time.Since(lastReceived).Round(time.Second))
	}
	fmt.Fprintf(w, "keep chats archived: %v\n", sm.db.KeepArchived())
	fmt.Fprintf(w, "chats: %d shown, %d archived, %d hidden\n", len(shown), len(archived), len(hidden))

	sm.dumpChats(w, "CHATS", shown)
	sm.dumpChats(w, "ARCHIVED", archived)
	sm.dumpChats(w, "HIDDEN", hidden)
	sm.dumpUnread(w, append(shown, archived...))

	sm.appStateLogLock.Lock()
	fmt.Fprintf(w, "\n=== APP STATE EVENTS (%d) ===\n", len(sm.appStateLog))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tACTION TIME\tFULL SYNC\tVALUE\tRANGE LAST MESSAGE\tRANGE MSGS\tJID")
	for _, line := range sm.appStateLog {
		fmt.Fprintln(tw, line)
	}
	tw.Flush()
	sm.appStateLogLock.Unlock()
}

// logAppState records an app state event for Dump.
func (sm *SessionManager) logAppState(kind string, jid types.JID, timestamp time.Time, fullSync bool, value string, msgRange *waSyncAction.SyncActionMessageRange) {
	if !sm.Headless {
		return
	}
	sm.appStateLogLock.Lock()
	defer sm.appStateLogLock.Unlock()
	sm.appStateLog = append(sm.appStateLog, fmt.Sprintf("%s\t%s\t%v\t%s\t%s\t%d\t%s",
		kind, timestamp.Format("2006-01-02 15:04:05"), fullSync, value,
		formatTimestamp(rangeTimestamp(msgRange)), len(msgRange.GetMessages()), jid))
}

// DumpChat writes the messages of a chat that are in memory.
func (sm *SessionManager) DumpChat(w io.Writer, chatID string) {
	client := sm.client()
	msgs := sm.db.GetMessages(chatID)
	fmt.Fprintf(w, "=== %s (%s): %d messages ===\n", sm.db.GetIdName(chatID), chatID, len(msgs))
	if picture := sm.chatPicture(chatID); picture != "" {
		fmt.Fprintf(w, "picture: %s\n", picture)
	}
	if isGroupID(chatID) {
		// they load in the background
		members := sm.GroupMembers(chatID)
		for start := time.Now(); members == nil && client != nil && client.IsConnected() && time.Since(start) < 10*time.Second; members = sm.GroupMembers(chatID) {
			time.Sleep(100 * time.Millisecond)
		}
		names := make([]string, len(members))
		for idx, member := range members {
			names[idx] = member.Name
		}
		fmt.Fprintf(w, "members to mention: %s\n", strings.Join(names, ", "))
	}
	// the alt texts of its images, which are made in the background
	sm.describeChat(chatID)
	for start := time.Now(); sm.altTextPending(chatID) && time.Since(start) < 10*time.Minute; {
		time.Sleep(time.Second)
	}
	msgs = sm.db.GetMessages(chatID)
	for _, msg := range msgs {
		sender := msg.ContactShort
		if msg.FromMe {
			sender = "Me"
		}
		if msg.ReplyTo != nil {
			fmt.Fprintf(w, "  replying to %s: %s\n", msg.ReplyTo.Name, strings.ReplaceAll(msg.ReplyTo.Text, "\n", " "))
		}
		if msg.FromMe && msg.Status != StatusUnknown {
			sender += " (" + msg.Status.String() + ")"
		}
		fmt.Fprintf(w, "%s %s: %s\n", formatTimestamp(int64(msg.Timestamp)), sender, strings.ReplaceAll(WithAltText(msg.Text, msg.AltText), "\n", " "))
		if len(msg.Reactions) > 0 {
			fmt.Fprintf(w, "  reactions: %s\n", strings.Join(sm.namedReactions(msg), ", "))
		}
	}
}

func (sm *SessionManager) dumpChats(w io.Writer, title string, chats []Chat) {
	fmt.Fprintf(w, "\n=== %s (%d) ===\n", title, len(chats))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tLAST MESSAGE\tLAST INCOMING\tPIN\tARCHIVED\tARCHIVED AT\tDELETED AT\tUNREAD\tMSGS\tID\tNAME\tWHATSMEOW SETTINGS")
	for idx, chat := range chats {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\n",
			idx+1,
			formatTimestamp(chat.LastMessage),
			formatTimestamp(chat.LastIncoming),
			yesNo(chat.Pinned),
			yesNo(chat.Archived),
			formatTimestamp(chat.ArchivedAt),
			formatTimestamp(chat.DeletedAt),
			chat.Unread,
			len(sm.db.GetMessages(chat.Id)),
			chat.Id,
			chat.Name,
			sm.whatsmeowChatSettings(chat.Id),
		)
	}
	tw.Flush()
}

func (sm *SessionManager) dumpUnread(w io.Writer, chats []Chat) {
	fmt.Fprintf(w, "\n=== UNREAD MESSAGES ===\n")
	for _, chat := range chats {
		var unread []Message
		for _, msg := range sm.db.GetMessages(chat.Id) {
			if msg.Unread {
				unread = append(unread, msg)
			}
		}
		if len(unread) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s (%s), %d unread:\n", chat.Name, chat.Id, len(unread))
		for _, msg := range unread {
			text := strings.ReplaceAll(msg.Text, "\n", " ")
			fmt.Fprintf(w, "  %s %s: %s\n", formatTimestamp(int64(msg.Timestamp)), msg.ContactShort, text)
		}
	}
}

// whatsmeowChatSettings describes the pinned and archived state that whatsmeow
// stored for a chat, under its phone number and LID.
func (sm *SessionManager) whatsmeowChatSettings(chatID string) string {
	client := sm.client()
	if client == nil || client.Store.ChatSettings == nil {
		return ""
	}
	jid, err := types.ParseJID(chatID)
	if err != nil {
		return ""
	}
	ctx := context.Background()
	jids := []types.JID{jid}
	if jid.Server == types.DefaultUserServer && client.Store.LIDs != nil {
		if lid, err := client.Store.LIDs.GetLIDForPN(ctx, jid); err == nil && !lid.IsEmpty() {
			jids = append(jids, lid)
		}
	}
	var out []string
	for _, jid := range jids {
		settings, err := client.Store.ChatSettings.GetChatSettings(ctx, jid)
		if err != nil || !settings.Found {
			continue
		}
		out = append(out, fmt.Sprintf("%s: pinned=%v archived=%v", jid.Server, settings.Pinned, settings.Archived))
	}
	return strings.Join(out, ", ")
}

func formatTimestamp(timestamp int64) string {
	if timestamp <= 0 {
		return "-"
	}
	return time.Unix(timestamp, 0).Format("2006-01-02 15:04")
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "-"
}
