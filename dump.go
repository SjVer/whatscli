package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/normen/whatscli/messages"
	"github.com/rs/zerolog"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// openLog returns a logger that writes debug output to path, or nil if path is empty.
func openLog(path string) (waLog.Logger, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %v", err)
	}
	log := zerolog.New(zerolog.ConsoleWriter{Out: file, NoColor: true, TimeFormat: time.DateTime}).
		Level(zerolog.DebugLevel).With().Timestamp().Logger()
	return waLog.Zerolog(log), nil
}

// consoleHandler prints status output to stderr instead of the UI, so that
// stdout only contains the dump.
type consoleHandler struct{}

func (c consoleHandler) NewMessage(msg messages.Message)         {}
func (c consoleHandler) NewScreen(msgs []messages.Message)       {}
func (c consoleHandler) SetChats(chats []messages.Chat)          {}
func (c consoleHandler) SetStatus(status messages.SessionStatus) {}
func (c consoleHandler) OpenFile(target string, command string)  {}
func (c consoleHandler) PrintError(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
}
func (c consoleHandler) PrintText(msg string) {
	fmt.Fprintln(os.Stderr, msg)
}
func (c consoleHandler) GetWriter() io.Writer {
	return os.Stderr
}

// runDump connects, waits until the chat list and offline messages are loaded,
// prints the state of whatscli and exits.
func runDump(offlineWait time.Duration, chatID string, logger waLog.Logger) int {
	sm := &messages.SessionManager{Headless: true, Log: logger}
	sm.Init(consoleHandler{})
	if err := sm.StartManager(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	defer sm.Close()

	select {
	case <-sm.ChatsLoaded:
	case <-sm.LoginFailed:
		return 1
	case <-time.After(time.Minute):
		fmt.Fprintln(os.Stderr, "error: timed out loading the chat list")
		return 1
	}
	select {
	case <-sm.OfflineSynced:
	case <-time.After(offlineWait):
		fmt.Fprintln(os.Stderr, "warning: offline messages were not fully delivered within", offlineWait)
	}

	if chatID != "" {
		return dumpChat(sm, chatID)
	}
	sm.Dump(os.Stdout)
	return 0
}

// dumpChat loads a chat from the phone like opening it in the UI does, and prints it.
func dumpChat(sm *messages.SessionManager, chatID string) int {
	start := time.Now()
	if err := sm.RequestChatHistory(chatID); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	for sm.HistoryPending(chatID) {
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "waited", time.Since(start).Round(time.Millisecond), "for the phone")
	sm.DumpChat(os.Stdout, chatID)
	return 0
}
