package messages

import (
	"context"
	"sort"
	"strings"
	"sync"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// Mentions: WhatsApp messages mention someone with @ and their number, or
// their LID in groups that address members by LID, in the text, and list who
// is mentioned. The phone shows the name instead of the number, and so does
// whatscli, see showMentions. Typing @Name mentions a member, see resolveMentions.

// Member is a member of a group who can be mentioned
type Member struct {
	// the JID the group knows the member by
	Id   string
	Name string
}

// groupMembers are the members of groups, loaded once per group from WhatsApp
type groupMembers struct {
	lock sync.Mutex
	// who can be mentioned, see GroupMembers
	members map[string][]Member
	// everyone but the user, by userKey, see Message.Receipts
	receivers map[string][]string
	loading   map[string]bool
}

// GroupMembers returns the members of a group that can be mentioned, without
// the user, sorted by name. They are loaded in the background the first time,
// and nil until then, or while not connected.
func (sm *SessionManager) GroupMembers(chatID string) []Member {
	members, _ := sm.loadedMembers(chatID)
	return members
}

// loadedMembers returns the members of a group that can be mentioned, and all
// members but the user, by userKey, if they are loaded, see GroupMembers
func (sm *SessionManager) loadedMembers(chatID string) ([]Member, []string) {
	if !isGroupID(chatID) {
		return nil, nil
	}
	sm.members.lock.Lock()
	defer sm.members.lock.Unlock()
	if members, ok := sm.members.members[chatID]; ok {
		return members, sm.members.receivers[chatID]
	}
	// the client can be replaced meanwhile, e.g. by /relink
	if client := sm.client; !sm.members.loading[chatID] && client != nil && client.IsConnected() {
		if sm.members.loading == nil {
			sm.members.loading = make(map[string]bool)
		}
		sm.members.loading[chatID] = true
		go sm.loadGroupMembers(client, chatID)
	}
	return nil, nil
}

// forgetMembers forgets the members of a group, which are loaded again, e.g.
// when someone joined, or of all groups for an empty chatID
func (sm *SessionManager) forgetMembers(chatID string) {
	sm.members.lock.Lock()
	defer sm.members.lock.Unlock()
	if chatID == "" {
		sm.members.members, sm.members.receivers = nil, nil
		return
	}
	delete(sm.members.members, chatID)
	delete(sm.members.receivers, chatID)
}

func (sm *SessionManager) loadGroupMembers(client *whatsmeow.Client, chatID string) {
	var members []Member
	var receivers []string
	jid, err := types.ParseJID(chatID)
	if err == nil {
		var info *types.GroupInfo
		if info, err = client.GetGroupInfo(context.Background(), jid); err == nil {
			members, receivers = sm.membersOf(info.Participants)
		}
	}
	sm.members.lock.Lock()
	delete(sm.members.loading, chatID)
	if err != nil {
		sm.members.lock.Unlock()
		sm.logWarn("Failed to load the members of %s: %v", chatID, err)
		return // tried again next time
	}
	if sm.members.members == nil {
		sm.members.members = make(map[string][]Member)
		sm.members.receivers = make(map[string][]string)
	}
	sm.members.members[chatID], sm.members.receivers[chatID] = members, receivers
	sm.members.lock.Unlock()

	// the receipts that arrived before are told apart by member now
	if sm.db.UpdateGroupStatuses(chatID, func(receipts map[string]MessageStatus) MessageStatus {
		return groupStatus(receipts, receivers)
	}) {
		sm.showIfOpen(chatID)
	}
}

// membersOf returns the participants of a group that can be mentioned, with
// their names, and all of them by userKey, without the user
func (sm *SessionManager) membersOf(participants []types.GroupParticipant) ([]Member, []string) {
	members := []Member{} // loaded, see GroupMembers
	var receivers []string
	for _, participant := range participants {
		if sm.isOwnUser(participant.JID) || sm.isOwnUser(participant.PhoneNumber) || sm.isOwnUser(participant.LID) {
			continue
		}
		lookup := participant.JID
		if !participant.PhoneNumber.IsEmpty() {
			lookup = participant.PhoneNumber // contacts are saved under the number
			// so that receipts by LID are kept by number too, see userKey
			sm.learnLID(participant.LID.String(), participant.PhoneNumber.String())
		}
		receivers = append(receivers, sm.userKey(participant.JID))
		name, ok := sm.realName(lookup)
		if !ok && lookup.Server != types.DefaultUserServer {
			continue // only their LID is known, which can't be told apart by name
		} else if !ok {
			name = DisplayID(lookup.String())
		}
		members = append(members, Member{Id: participant.JID.ToNonAD().String(), Name: name})
	}
	sort.Slice(members, func(i, j int) bool { return strings.ToLower(members[i].Name) < strings.ToLower(members[j].Name) })
	return members, receivers
}

// resolveMentions replaces @Name of the members of a group in text with how
// WhatsApp mentions them, and returns who is mentioned, and the mentions as
// they were typed.
func resolveMentions(text string, members []Member) (string, []string, []string) {
	// longer names first, so that @Ann Lee isn't taken for @Ann
	sorted := append([]Member(nil), members...)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i].Name) > len(sorted[j].Name) })
	var mentioned, typed []string
	for _, member := range sorted {
		jid, err := types.ParseJID(member.Id)
		if err != nil || !strings.Contains(text, "@"+member.Name) {
			continue
		}
		text = strings.ReplaceAll(text, "@"+member.Name, "@"+jid.User)
		mentioned = append(mentioned, member.Id)
		typed = append(typed, "@"+member.Name)
	}
	return text, mentioned, typed
}

// showMentions replaces the numbers of the mentioned people in the text of a
// message with their names, like the phone shows them, and returns the
// mentions as they are shown.
func (sm *SessionManager) showMentions(text string, mentioned []string) (string, []string) {
	var shown []string
	for _, id := range mentioned {
		jid, err := types.ParseJID(id)
		if err != nil || jid.User == "" {
			continue
		}
		// as the number when no name is known, like the phone does
		name, ok := "You", true
		if !sm.isOwnUser(jid) {
			name, ok = sm.realName(jid)
		}
		mention := "@" + jid.User
		if ok {
			text = strings.ReplaceAll(text, mention, "@"+name)
			mention = "@" + name
		}
		if strings.Contains(text, mention) {
			shown = append(shown, mention)
		}
	}
	return text, shown
}

// isOwnUser returns whether jid is the user, by number or LID
func (sm *SessionManager) isOwnUser(jid types.JID) bool {
	if sm.client == nil || sm.client.Store.ID == nil {
		return false
	}
	lid := sm.client.Store.GetLID()
	return jid.User == sm.client.Store.ID.User || !lid.IsEmpty() && jid.User == lid.User
}
