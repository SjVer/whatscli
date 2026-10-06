package messages

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/normen/whatscli/config"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// SessionManager deals with the connection and receives commands from the UI.
type SessionManager struct {
	db *MessageDatabase
	// the open chat, see openChat
	currentReceiver string
	receiverLock    sync.RWMutex
	uiHandler       UiMessageHandler
	// the connection to WhatsApp, which is replaced by /connect and /logout, see client
	clientPtr      atomic.Pointer[whatsmeow.Client]
	container      *sqlstore.Container
	cacheContainer *sqlstore.Container
	StatusChannel  chan struct{}
	CommandChannel chan Command
	statusInfo     SessionStatus
	started        bool
	eventHandler   *eventHandler
	// Headless prevents logging in with a QR code, for running without the UI.
	Headless bool
	// Log receives the log output of whatsmeow, nothing is logged if it is nil.
	Log waLog.Logger
	// ChatSeen returns whether the user is looking at the open chat, so that the
	// messages that arrive in it are read, see seesChat. Without it, they are.
	ChatSeen func() bool
	// ChatsLoaded is signalled when the chat list was loaded after connecting.
	ChatsLoaded chan struct{}
	// OfflineSynced is signalled when the messages received while offline were delivered.
	OfflineSynced chan struct{}
	// LoginFailed receives the error when connecting at startup fails.
	LoginFailed chan error

	// app state events received in headless mode, shown by Dump
	appStateLog     []string
	appStateLogLock sync.Mutex

	// the running /relink, or nil
	chatSync     *chatSync
	chatSyncLock sync.Mutex

	// chats whose messages were requested from the phone
	history historyRequests
	// guards statusInfo, which is also changed outside the manager loop
	statusLock sync.Mutex
	// when data was last received from WhatsApp, in nanoseconds, see LastReceived
	lastReceived atomic.Int64
	// notifies when the connection is lost
	connection connectionWatch
	// how many messages that arrived while whatscli was closed are being delivered,
	// 0 when they were, see startOfflineSync
	offlineMessages atomic.Int64
	// pictures of chats shown on notifications
	pictures chatPictures
	// the model that describes images, and the messages waiting for it, see describeChat
	altServer altServer
	altTexts  altTexts
	ffmpeg    ffmpegTool
	// members of groups who can be mentioned
	members groupMembers
	// whether the QR code is shown, and whatscli can be linked, see LinkWithCode
	linking atomic.Bool
	// when app state collections were asked from the phone, see recoverAppState
	recoveryRequested map[appstate.WAPatchName]time.Time
	recoveryLock      sync.Mutex
}

// Init initializes the SessionManager.
func (sm *SessionManager) Init(handler UiMessageHandler) {
	sm.db = &MessageDatabase{}
	sm.db.Init()
	sm.uiHandler = handler
	if err := sm.db.LoadChats(config.GetSessionFilePath()+".chats.json", historyCount()); err != nil {
		handler.PrintError(fmt.Errorf("failed to load saved chats: %v", err))
	}
	sm.StatusChannel = make(chan struct{}, 10)
	sm.CommandChannel = make(chan Command, 10)
	sm.eventHandler = &eventHandler{sm: sm}
	sm.ChatsLoaded = make(chan struct{}, 1)
	sm.OfflineSynced = make(chan struct{}, 1)
	sm.LoginFailed = make(chan error, 1)
}

// Close disconnects from WhatsApp and saves the chat list.
func (sm *SessionManager) Close() {
	client := sm.client()
	if client != nil {
		client.Disconnect()
	}
	sm.stopServer()
	sm.db.saveChats()
}

// signal sends value to ch without blocking when nobody is waiting.
func signal[T any](ch chan T, value T) {
	select {
	case ch <- value:
	default:
	}
}

// StartManager starts the receiver and message handling goroutine.
func (sm *SessionManager) StartManager() error {
	if sm.started {
		return errors.New("session manager running, send commands to control")
	}
	sm.started = true
	go sm.cleanPreviews()
	go sm.runManager()
	return nil
}

// runManager logs in, and then runs the commands of the UI and shows the
// state of the connection, as long as whatscli runs
func (sm *SessionManager) runManager() {
	client, err := sm.getConnection()
	if err == nil {
		err = sm.loginWithConnection(client)
	} else {
		err = fmt.Errorf("failed to create WhatsApp connection: %v", err)
	}
	if err != nil {
		sm.uiHandler.PrintError(err)
		signal(sm.LoginFailed, err)
	}

	for {
		select {
		case command := <-sm.CommandChannel:
			sm.execCommand(command)
		case <-sm.StatusChannel:
			sm.statusLock.Lock()
			prevStatus := sm.statusInfo.Connected
			_, err := sm.connectedClient()
			sm.statusInfo.Connected = err == nil
			status := sm.statusInfo
			sm.statusLock.Unlock()
			sm.uiHandler.SetStatus(status)
			if prevStatus != status.Connected {
				if status.Connected {
					// the status bar says so
					sm.uiHandler.SetNotice("", connectionNotice, "")
				} else {
					sm.uiHandler.SetNotice("", connectionNotice, "Disconnected")
				}
			}
		}
	}
}

// client returns the connection to WhatsApp, or nil. Code that runs beside
// the manager loop takes one copy, as /connect and /logout replace it.
func (sm *SessionManager) client() *whatsmeow.Client {
	return sm.clientPtr.Load()
}

// errNotConnected is returned when WhatsApp can't be reached
var errNotConnected = errors.New("not connected to WhatsApp")

// connectedClient returns the connection to WhatsApp if it is connected
func (sm *SessionManager) connectedClient() (*whatsmeow.Client, error) {
	if client := sm.client(); client != nil && client.IsConnected() {
		return client, nil
	}
	return nil, errNotConnected
}

// openChat returns the ID of the open chat, "" if none is
func (sm *SessionManager) openChat() string {
	sm.receiverLock.RLock()
	defer sm.receiverLock.RUnlock()
	return sm.currentReceiver
}

// sendStatus tells the manager loop that the connection changed, without
// waiting: it reads the state of the connection itself, so one message is enough
func (sm *SessionManager) sendStatus() {
	signal(sm.StatusChannel, struct{}{})
}

func (sm *SessionManager) setCurrentReceiver(id string) {
	sm.receiverLock.Lock()
	sm.currentReceiver = id
	sm.receiverLock.Unlock()
	sm.uiHandler.NewScreen(id, sm.db.GetMessages(id))
	// only the newest messages are saved, load the ones before them from the phone
	if id != "" {
		sm.loadChatOnce(id)
		sm.GroupMembers(id) // to suggest them for mentions
		sm.describeChat(id)
	}
}

// keys of the notices on the main screen, see UiMessageHandler.SetNotice
const (
	connectionNotice = "connection"
	contactsNotice   = "contacts"
	appStateNotice   = "appstate"
)
