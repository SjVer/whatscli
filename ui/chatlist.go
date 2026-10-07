package ui

import (
	_ "embed"
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

// colors an entry of the chat list, on the configured background
func setNodeColor(node *tview.TreeNode, color tcell.Color) *tview.TreeNode {
	node.SetColor(color)
	return node.SetTextStyle(node.GetTextStyle().Background(tcell.ColorNames[config.Config.Colors.Background]))
}

// creates the TreeView for chats
func MakeTree() *tview.TreeView {
	rootDir := "Chats"
	chatRoot = setNodeColor(tview.NewTreeNode(rootDir), tcell.ColorNames[config.Config.Colors.ListHeader])
	treeView = tview.NewTreeView().
		SetRoot(chatRoot).
		SetCurrentNode(chatRoot)
	treeView.SetBackgroundColor(tcell.ColorNames[config.Config.Colors.Background])

	// If a chat was selected, open it.
	treeView.SetChangedFunc(func(node *tview.TreeNode) {
		// the root and the archived chats folder show no chat, so that
		// collapsing the folder isn't undone for the open archived chat
		recv, _ := node.GetReference().(messages.Chat)
		if recv.Id == currentReceiver.Id {
			// the chat list was shown again, with new nodes
			currentReceiver = recv
			return
		}
		SetDisplayedChat(recv)
	})
	// Collapse or expand the archived chats folder when it is selected.
	treeView.SetSelectedFunc(func(node *tview.TreeNode) {
		if len(node.GetChildren()) > 0 && node != chatRoot {
			archivedExpanded = !node.IsExpanded()
			node.SetExpanded(archivedExpanded)
		}
	})
	return treeView
}

// goes back to Chats, the root of the chat list, and closes the archived chats
func showChatsRoot() {
	archivedExpanded = false
	for _, node := range chatRoot.GetChildren() {
		if node.GetReference() == "archived" {
			node.SetExpanded(false)
		}
	}
	if treeView.GetCurrentNode() != chatRoot {
		treeView.SetCurrentNode(chatRoot)
		SetDisplayedChat(messages.Chat{})
	}
}

// sets the current chat, loads text from storage to TextView
func SetDisplayedChat(wid messages.Chat) {
	if wid.Id != currentReceiver.Id {
		// the reply and the image are of the other chat
		cancelReply()
		removePastedImage()
		setInput(switchDraft(currentReceiver.Id, wid.Id, textInput.GetText()))
		updateChatNode(currentReceiver.Id)
		updateChatNode(wid.Id)
		// the message to react to is in the other chat
		reactTarget = ""
		// a chat opens at its newest messages, also after scrolling up in the one before
		ResetMsgSelection()
	}
	currentReceiver = wid
	chatMessages, curRegions, endedWithReaction = nil, nil, false
	messageSearch = ""
	textView.Clear()
	printedSinceRender.Store(false)
	sendCommand(messages.Command{Name: "select", Params: []string{currentReceiver.Id}})
}

// shows the chats in the chat list, or the ones matching the search, including
// archived chats and contacts without messages
func renderChats() {
	// the nodes are new, so the selection is moved to them
	folderSelected := treeView.GetCurrentNode() != nil && treeView.GetCurrentNode().GetReference() == "archived"
	chatRoot.ClearChildren()
	archivedNode := setNodeColor(tview.NewTreeNode("Archived"), tcell.ColorNames[config.Config.Colors.ListHeader]).
		SetReference("archived").
		SetSelectable(true).
		SetExpanded(archivedExpanded)
	if folderSelected {
		treeView.SetCurrentNode(archivedNode)
	}
	oldId := currentReceiver.Id
	for _, element := range allChats {
		if chatSearch != "" {
			if !chatMatches(element, chatSearch) {
				continue
			}
		} else if element.Hidden {
			continue
		}
		node := tview.NewTreeNode(chatNodeText(element)).
			SetReference(element).
			SetSelectable(true)
		if element.IsGroup {
			setNodeColor(node, tcell.ColorNames[config.Config.Colors.ListGroup])
		} else {
			setNodeColor(node, tcell.ColorNames[config.Config.Colors.ListContact])
		}
		// store new currentReceiver, else the selection on the left goes off
		if element.Id == oldId {
			currentReceiver = element
		}
		if element.InArchive && chatSearch == "" {
			archivedNode.AddChild(node)
		} else {
			chatRoot.AddChild(node)
		}
		if element.Id == currentReceiver.Id {
			if element.InArchive {
				archivedExpanded = true
				archivedNode.SetExpanded(true)
			}
			treeView.SetCurrentNode(node)
		}
	}
	if count := len(archivedNode.GetChildren()); count > 0 {
		archivedNode.SetText(fmt.Sprintf("Archived (%d)", count))
		chatRoot.AddChild(archivedNode)
	}
	if chatSearch != "" {
		chatRoot.SetText(fmt.Sprintf("Chats with \"%s\" (%d)", tview.Escape(chatSearch), len(chatRoot.GetChildren())))
	} else {
		chatRoot.SetText("Chats")
	}
}

// chatNodeText returns the text of a chat in the chat list
func chatNodeText(chat messages.Chat) string {
	name := chat.Name
	if name == "" {
		name = messages.DisplayID(chat.Id)
	}
	name = nameText(chat.Id, name)
	if chat.Pinned {
		name = "📌 " + name
	}
	if drafts[chat.Id] != "" {
		name += " ✎"
	}
	if count := chat.NewCount(); count > 0 {
		// bold, so that isUnreadCount can tell it from headers in the same color
		name += " ([" + config.Config.Colors.UnreadCount + "::b]" + fmt.Sprint(count) + "[-::-])"
	}
	// search results show archived chats among the others
	if chat.InArchive && chatSearch != "" {
		name += " [::d](archived)[::-]"
	}
	return name
}

// updateChatNode updates the text of a chat in the chat list, e.g. when its draft changed
func updateChatNode(chatID string) {
	chatRoot.Walk(func(node, parent *tview.TreeNode) bool {
		if chat, ok := node.GetReference().(messages.Chat); ok && chat.Id == chatID {
			node.SetText(chatNodeText(chat))
		}
		return true
	})
}
