package messages

import (
	"context"
	"sort"
	"strings"
	"sync"

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
	lock    sync.Mutex
	members map[string][]Member
	loading map[string]bool
}

// GroupMembers returns the members of a group that can be mentioned, without
// the user, sorted by name. They are loaded in the background the first time,
// and nil until then.
func (sm *SessionManager) GroupMembers(chatID string) []Member {
	if !strings.HasSuffix(chatID, GROUPSUFFIX) {
		return nil
	}
	sm.members.lock.Lock()
	defer sm.members.lock.Unlock()
	if members, ok := sm.members.members[chatID]; ok {
		return members
	}
	if !sm.members.loading[chatID] && sm.client != nil && sm.client.IsConnected() {
		if sm.members.loading == nil {
			sm.members.loading = make(map[string]bool)
		}
		sm.members.loading[chatID] = true
		go sm.loadGroupMembers(chatID)
	}
	return nil
}

func (sm *SessionManager) loadGroupMembers(chatID string) {
	var members []Member
	jid, err := types.ParseJID(chatID)
	if err == nil {
		var info *types.GroupInfo
		if info, err = sm.client.GetGroupInfo(context.Background(), jid); err == nil {
			members = sm.membersOf(info.Participants)
		}
	}
	sm.members.lock.Lock()
	defer sm.members.lock.Unlock()
	delete(sm.members.loading, chatID)
	if err != nil {
		return // tried again next time
	}
	if sm.members.members == nil {
		sm.members.members = make(map[string][]Member)
	}
	sm.members.members[chatID] = members
}

// membersOf returns the participants of a group with their names, without the user
func (sm *SessionManager) membersOf(participants []types.GroupParticipant) []Member {
	var members []Member
	for _, participant := range participants {
		if sm.isOwnUser(participant.JID) || sm.isOwnUser(participant.PhoneNumber) || sm.isOwnUser(participant.LID) {
			continue
		}
		lookup := participant.JID
		if !participant.PhoneNumber.IsEmpty() {
			lookup = participant.PhoneNumber // contacts are saved under the number
		}
		_, name, _ := sm.contactNames(lookup)
		if name == "" || strings.Contains(name, "@") {
			name = lookup.User
		}
		members = append(members, Member{Id: participant.JID.ToNonAD().String(), Name: name})
	}
	sort.Slice(members, func(i, j int) bool { return strings.ToLower(members[i].Name) < strings.ToLower(members[j].Name) })
	return members
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
		name := "You"
		if !sm.isOwnUser(jid) {
			_, name, _ = sm.contactNames(jid)
		}
		mention := "@" + jid.User
		if name != "" && !strings.Contains(name, "@") {
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
