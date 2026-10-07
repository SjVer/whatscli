package ui

import (
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// version is the version of whatscli shown in the top bar, see Run
var version string

var currentReceiver messages.Chat = messages.Chat{}

var curRegions []messages.Message

var textView *tview.TextView

var treeView *tview.TreeView

var textInput *tview.TextArea

var topBar *tview.TextView

var infoBar *tview.TextView

var chatRoot *tview.TreeNode

var archivedExpanded bool

// all chats as last set by the session manager, and the text the chat list is filtered by
var allChats []messages.Chat

var chatSearch string

// the message that /react reacts to, selected with the react key
var reactTarget string

// all messages of the displayed chat, and the text they are filtered by
var chatMessages []messages.Message

var messageSearch string

var app *tview.Application

var sessionManager *messages.SessionManager

// Run shows the UI of whatscli, of the given version, until it is quit, with
// the log of the connection to WhatsApp written to logger if it isn't nil
func Run(appVersion string, logger waLog.Logger) {
	version = appVersion
	sessionManager = &messages.SessionManager{Log: logger}
	sessionManager.Init(UiHandler{})

	app = tview.NewApplication()
	// a screen of its own, which notes when whatscli has focus, see chatSeen
	if screen, err := newFocusScreen(); err == nil {
		app.SetScreen(screen) // which starts it
		screen.EnableFocus()
	} else if logger != nil {
		logger.Warnf("Failed to open the screen, focus isn't noted: %v", err)
	}
	sessionManager.ChatSeen = chatSeen

	sideBarWidth := config.Config.Ui.ChatSidebarWidth
	gridLayout := tview.NewGrid()
	gridLayout.SetRows(1, 0, 1)
	gridLayout.SetColumns(sideBarWidth, 0, sideBarWidth)
	gridLayout.SetBorders(true)
	gridLayout.SetBackgroundColor(tcell.ColorNames[config.Config.Colors.Background])
	gridLayout.SetBordersColor(tcell.ColorNames[config.Config.Colors.Borders])

	cmdPrefix := config.Config.General.CmdPrefix
	topBar = tview.NewTextView()
	topBar.SetDynamicColors(true)
	topBar.SetScrollable(false)
	topBar.SetText("[::b] WhatsCLI " + version + "  [-::d]Type " + cmdPrefix + "help or press " + config.Config.Keymap.CommandHelp + " for help")
	topBar.SetBackgroundColor(tcell.ColorNames[config.Config.Colors.Background])

	infoBar = tview.NewTextView()
	infoBar.SetDynamicColors(true)
	infoBar.SetBackgroundColor(tcell.ColorNames[config.Config.Colors.Background])
	UpdateStatusBar(messages.SessionStatus{})

	textView = tview.NewTextView().
		SetDynamicColors(true).
		SetRegions(true).
		SetWordWrap(true)
	textView.SetBackgroundColor(tcell.ColorNames[config.Config.Colors.Background])
	textView.SetMouseCapture(scrollWithWheel)
	textView.SetTextColor(tcell.ColorNames[config.Config.Colors.Text])

	PrintHelp()

	textInput = newTextInput()
	loadRecentEmoji()

	gridLayout.AddItem(topBar, 0, 0, 1, 4, 0, 0, false)
	gridLayout.AddItem(infoBar, 2, 0, 1, 1, 0, 0, false)
	gridLayout.AddItem(MakeTree(), 1, 0, 1, 1, 0, 0, false)
	gridLayout.AddItem(textView, 1, 1, 1, 3, 0, 0, false)
	gridLayout.AddItem(textInput, 2, 1, 1, 3, 0, 0, false)

	app.SetRoot(gridLayout, true)
	app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		updateInputHeight(gridLayout)
		return false
	})
	app.SetAfterDrawFunc(func(screen tcell.Screen) {
		greyOutUnfocusedPanel(screen)
		highlightInputMentions(screen)
		drawSuggestions(screen)
		updateTitle(screen)
		chatListFocused.Store(treeView.HasFocus())
		// the panel is sized when it is drawn
		if messageWidth() != renderedWidth && !printedSinceRender.Load() && !widthRenderQueued {
			widthRenderQueued = true
			go app.QueueUpdateDraw(renderForWidth)
		}
	})
	app.EnableMouse(true)
	// pasted text arrives in one piece, so line breaks don't send it line by line
	app.EnablePaste(true)
	app.SetFocus(textInput)
	if err := sessionManager.StartManager(); err != nil {
		PrintError(err)
	}
	LoadShortcuts()
	// keeps how long ago the last sync was up to date
	go func() {
		for range time.Tick(time.Second) {
			app.QueueUpdateDraw(func() {
				UpdateStatusBar(lastStatus)
			})
		}
	}()
	app.Run()
	// saves changes of the last second too
	sessionManager.Close()
}
