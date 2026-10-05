package main

import (
	_ "embed"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

// turns all text grey in the chat panel while the chat list has focus, and in
// the chat list otherwise
func greyOutUnfocusedPanel(screen tcell.Screen) {
	var unfocused *tview.Box
	if treeView.HasFocus() {
		unfocused = textView.Box
	} else {
		unfocused = treeView.Box
	}
	x, y, width, height := unfocused.GetInnerRect()
	for cy := y; cy < y+height; cy++ {
		for cx := x; cx < x+width; cx++ {
			mainc, combc, style, _ := screen.GetContent(cx, cy)
			// unread counts keep their color, to still stand out. Dim text, like
			// notices, is as grey as the rest, as it would be too dark to read.
			if !isUnreadCount(style) {
				screen.SetContent(cx, cy, mainc, combc, style.Foreground(tcell.ColorGray).Dim(false))
			}
		}
	}
}

// isUnreadCount returns whether a cell of the chat list shows an unread count
func isUnreadCount(style tcell.Style) bool {
	fg, _, attr := style.Decompose()
	return fg == tcell.ColorNames[config.Config.Colors.UnreadCount] && attr&tcell.AttrBold != 0
}

// updates the status bar
func UpdateStatusBar(statusInfo messages.SessionStatus) {
	lastStatus = statusInfo
	out := " "
	if statusInfo.Connected {
		out += "[" + config.Config.Colors.Positive + "]online[-]"
	} else {
		out += "[" + config.Config.Colors.Negative + "]offline[-]"
	}
	out += " "
	// the status bar is narrow, show what whatscli is waiting for instead of the rest
	if statusInfo.Activity != "" {
		infoBar.SetText(out + "[::d]" + statusInfo.Activity + "[::-]")
		return
	}
	out += syncText(statusInfo.Connected, sessionManager.LastReceived(), time.Now())
	infoBar.SetText(out)
}

// the status last shown in the status bar, shown again every second, see syncText
var lastStatus messages.SessionStatus

// syncText tells how long ago data was last received from WhatsApp. It is red
// when that was over a minute ago while connected, as the answers to keepalive
// pings come every 20 to 30 seconds, and the connection seems to be lost.
func syncText(connected bool, lastReceived time.Time, now time.Time) string {
	if lastReceived.IsZero() {
		return ""
	}
	since := now.Sub(lastReceived)
	color := "gray"
	if connected && since > time.Minute {
		color = config.Config.Colors.Negative
	}
	return "[" + color + "]synced " + timeAgo(since) + "[-]"
}

// timeAgo returns how long ago something was, like "just now" or "5 min ago"
func timeAgo(duration time.Duration) string {
	plural := func(count int, unit string) string {
		if count == 1 {
			return fmt.Sprintf("1 %s ago", unit)
		}
		return fmt.Sprintf("%d %ss ago", count, unit)
	}
	switch {
	case duration < time.Minute:
		return "just now"
	case duration < time.Hour:
		return fmt.Sprintf("%d min ago", int(duration.Minutes()))
	case duration < 24*time.Hour:
		return plural(int(duration.Hours()), "hour")
	default:
		return plural(int(duration.Hours()/24), "day")
	}
}

// the title of the terminal last set, see updateTitle
var shownTitle string

// windowTitle returns the title of the terminal: the number of new messages and
// reactions in the chats that aren't archived, like the phone counts them
func windowTitle(chats []messages.Chat) string {
	count := 0
	for _, chat := range chats {
		if !chat.Hidden && !chat.InArchive {
			count += chat.NewCount()
		}
	}
	if count > 0 {
		return fmt.Sprintf("WhatsCLI (%d)", count)
	}
	return "WhatsCLI"
}

// updateTitle shows the number of new messages in the terminal title, e.g. its tab
func updateTitle(screen tcell.Screen) {
	if title := windowTitle(allChats); title != shownTitle {
		shownTitle = title
		screen.SetTitle(title)
	}
}
