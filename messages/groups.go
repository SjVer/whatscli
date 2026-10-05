package messages

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

func (sm *SessionManager) leaveCurrentGroup() {
	client, groupJID, err := sm.currentGroup()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	if err = client.LeaveGroup(context.Background(), groupJID); err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	sm.uiHandler.PrintText("left group " + groupJID.String())
}

func (sm *SessionManager) createGroup(params []string) {
	if !checkParam(params, 1) {
		sm.printCommandUsage("create", "[user-id[] [user-id[] New Group Subject")
		sm.printCommandUsage("create", "New Group Subject")
		return
	}

	participants := make([]types.JID, 0)
	idx := 0
	for idx < len(params) && strings.Contains(params[idx], CONTACTSUFFIX) {
		participant, err := types.ParseJID(params[idx])
		if err != nil {
			sm.uiHandler.PrintError(fmt.Errorf("invalid user id %q: %v", params[idx], err))
			return
		}
		participants = append(participants, participant)
		idx++
	}

	name := strings.Join(params[idx:], " ")
	if name == "" {
		name = strings.Join(params, " ")
		participants = nil
	}

	client, err := sm.connectedClient()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	groupInfo, err := client.CreateGroup(context.Background(), whatsmeow.ReqCreateGroup{
		Name:         name,
		Participants: participants,
	})
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}

	sm.db.AddChat(Chat{
		Id:          groupInfo.JID.String(),
		IsGroup:     true,
		Name:        groupInfo.Name,
		LastMessage: time.Now().Unix(),
	})
	sm.uiHandler.SetChats(sm.db.GetChatIds())
	sm.uiHandler.PrintText("created new group " + groupInfo.JID.String())
}

func (sm *SessionManager) updateCurrentGroupParticipants(params []string, action whatsmeow.ParticipantChange, command, success string) {
	client, groupJID, err := sm.currentGroup()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	if !checkParam(params, 1) {
		sm.printCommandUsage(command, "[user-id[]")
		return
	}

	participants := make([]types.JID, 0, len(params))
	for _, raw := range params {
		jid, err := types.ParseJID(raw)
		if err != nil {
			sm.uiHandler.PrintError(fmt.Errorf("invalid user id %q: %v", raw, err))
			return
		}
		participants = append(participants, jid)
	}

	if _, err = client.UpdateGroupParticipants(context.Background(), groupJID, participants, action); err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	sm.uiHandler.PrintText(success + " for " + groupJID.String())
}

func (sm *SessionManager) updateCurrentGroupSubject(params []string) {
	client, groupJID, err := sm.currentGroup()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	if !checkParam(params, 1) {
		sm.printCommandUsage("subject", "new-subject -> in group chat")
		return
	}

	name := strings.Join(params, " ")
	if err = client.SetGroupName(context.Background(), groupJID, name); err != nil {
		sm.uiHandler.PrintError(err)
		return
	}

	sm.db.AddChat(Chat{
		Id:      groupJID.String(),
		IsGroup: true,
		Name:    name,
	})
	sm.uiHandler.SetChats(sm.db.GetChatIds())
	sm.uiHandler.PrintText("updated subject for " + groupJID.String())
}

// currentGroup returns the connection to WhatsApp and the open chat, if it is a group
func (sm *SessionManager) currentGroup() (*whatsmeow.Client, types.JID, error) {
	chatID := sm.openChat()
	if !isGroupID(chatID) {
		return nil, types.JID{}, errors.New("not a group")
	}
	client, err := sm.connectedClient()
	if err != nil {
		return nil, types.JID{}, err
	}
	jid, err := types.ParseJID(chatID)
	return client, jid, err
}
