package messages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	_ "github.com/mattn/go-sqlite3" // SQLite driver
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/qrcode"
	"github.com/rivo/tview"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

var urlPattern = regexp.MustCompile(`https?://[^\s]+`)

// SessionManager deals with the connection and receives commands from the UI.
type SessionManager struct {
	db              *MessageDatabase
	currentReceiver string
	uiHandler       UiMessageHandler
	client          *whatsmeow.Client
	container       *sqlstore.Container
	cacheContainer  *sqlstore.Container
	StatusChannel   chan StatusMsg
	CommandChannel  chan Command
	ChatChannel     chan Chat
	ContactChannel  chan Contact
	TextChannel     chan *waProto.Message
	statusInfo      SessionStatus
	lastSent        time.Time
	started         bool
	eventHandler    *eventHandler
	// Headless prevents logging in with a QR code, for running without the UI.
	Headless bool
	// Log receives the log output of whatsmeow, nothing is logged if it is nil.
	Log waLog.Logger
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
	// pictures of chats shown on notifications
	pictures chatPictures
	// app state collections that were asked from the phone, see recoverAppState
	recoveryRequested map[appstate.WAPatchName]bool
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
	sm.StatusChannel = make(chan StatusMsg, 10)
	sm.CommandChannel = make(chan Command, 10)
	sm.ChatChannel = make(chan Chat, 10)
	sm.ContactChannel = make(chan Contact, 10)
	sm.TextChannel = make(chan *waProto.Message, 10)
	sm.eventHandler = &eventHandler{sm: sm}
	sm.ChatsLoaded = make(chan struct{}, 1)
	sm.OfflineSynced = make(chan struct{}, 1)
	sm.LoginFailed = make(chan error, 1)
}

// Close disconnects from WhatsApp and saves the chat list.
func (sm *SessionManager) Close() {
	if sm.client != nil {
		sm.client.Disconnect()
	}
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
	go sm.runManager()
	return nil
}

func (sm *SessionManager) runManager() error {
	client, err := sm.getConnection()
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to create WhatsApp connection: %v", err))
		return err
	}
	if client == nil {
		return errors.New("could not establish WhatsApp connection")
	}

	if err = sm.loginWithConnection(client); err != nil {
		sm.uiHandler.PrintError(err)
		signal(sm.LoginFailed, err)
	}

	for sm.started {
		select {
		case command := <-sm.CommandChannel:
			sm.execCommand(command)
		case statusMsg := <-sm.StatusChannel:
			sm.statusLock.Lock()
			prevStatus := sm.statusInfo.Connected
			if statusMsg.err == nil {
				sm.statusInfo.Connected = statusMsg.connected
			}
			if sm.client != nil {
				sm.statusInfo.Connected = sm.client.IsConnected()
			} else {
				sm.statusInfo.Connected = false
			}
			status := sm.statusInfo
			sm.statusLock.Unlock()
			sm.uiHandler.SetStatus(status)
			if prevStatus != status.Connected {
				if status.Connected {
					sm.uiHandler.PrintText("connected")
				} else {
					sm.uiHandler.PrintText("disconnected")
				}
			}
		}
	}

	fmt.Fprintln(sm.uiHandler.GetWriter(), "closing the receiver")
	if sm.client != nil {
		sm.client.Disconnect()
	}
	return nil
}

func (sm *SessionManager) setCurrentReceiver(id string) {
	sm.currentReceiver = id
	sm.uiHandler.NewScreen(sm.getMessages(id))
	// only the newest messages are saved, load the ones before them from the phone
	if id != "" {
		sm.loadChatOnce(id)
	}
}

func (sm *SessionManager) getConnection() (*whatsmeow.Client, error) {
	if sm.client == nil {
		dbPath := config.GetSessionFilePath() + ".db"
		container, err := sqlstore.New(context.Background(), "sqlite3", "file:"+dbPath+"?_foreign_keys=on", waLog.Noop)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to database: %v", err)
		}
		deviceStore, err := container.GetFirstDevice(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to get device: %v", err)
		}
		if deviceStore.ID != nil {
			if err = sm.useCacheStore(deviceStore); err != nil {
				return nil, err
			}
		}
		logger := sm.Log
		if logger == nil {
			logger = waLog.Noop
		}
		client := whatsmeow.NewClient(deviceStore, activityLogger{Logger: logger, received: sm.noteReceived})
		// needed so loadRecentChats gets chat timestamps from the app state
		client.EmitAppStateEventsOnFullSync = true
		client.AddEventHandler(sm.eventHandler.Handle)
		sm.client = client
		sm.container = container
	}
	return sm.client, nil
}

// useCacheStore moves the parts of the device store that WhatsApp can sync again
// (contacts, chat settings and app state) to a separate database, so that it can
// be deleted without logging out. Keys, sessions and message secrets stay in the
// session database.
func (sm *SessionManager) useCacheStore(device *store.Device) error {
	if sm.cacheContainer == nil {
		// whatsmeow only creates the tables with foreign keys enabled, but the
		// device row they reference is in the session database, so reopen without
		cachePath := "file:" + config.GetSessionFilePath() + ".cache.db"
		upgraded, err := sqlstore.New(context.Background(), "sqlite3", cachePath+"?_foreign_keys=on", waLog.Noop)
		if err != nil {
			return fmt.Errorf("failed to open cache database: %v", err)
		}
		upgraded.Close()
		db, err := sql.Open("sqlite3", cachePath+"?_foreign_keys=off")
		if err != nil {
			return fmt.Errorf("failed to open cache database: %v", err)
		}
		// Without foreign keys, deleting an app state version (as full syncs do) no longer
		// cascades to its mutation MACs, so do that with a trigger and remove leftovers.
		_, err = db.Exec(`
			CREATE TRIGGER IF NOT EXISTS whatscli_delete_app_state_macs
			AFTER DELETE ON whatsmeow_app_state_version
			BEGIN
				DELETE FROM whatsmeow_app_state_mutation_macs WHERE jid=OLD.jid AND name=OLD.name;
			END;
			DELETE FROM whatsmeow_app_state_mutation_macs
			WHERE (jid, name) NOT IN (SELECT jid, name FROM whatsmeow_app_state_version);
		`)
		if err != nil {
			db.Close()
			return fmt.Errorf("failed to set up cache database: %v", err)
		}
		sm.cacheContainer = sqlstore.NewWithDB(db, "sqlite3", waLog.Noop)
	}
	cache := sqlstore.NewSQLStore(sm.cacheContainer, *device.ID)
	device.Contacts = cache
	device.ChatSettings = cache
	device.AppState = cache
	return nil
}

// removeCacheStore closes and deletes the cache database.
func (sm *SessionManager) removeCacheStore() {
	if sm.cacheContainer != nil {
		sm.cacheContainer.Close()
		sm.cacheContainer = nil
	}
	if err := os.Remove(config.GetSessionFilePath() + ".cache.db"); err != nil && !os.IsNotExist(err) {
		sm.uiHandler.PrintText("Warning: Couldn't remove cache database: " + err.Error())
	}
}

func (sm *SessionManager) login() error {
	sm.client = nil
	client, err := sm.getConnection()
	if err != nil {
		return fmt.Errorf("failed to create WhatsApp connection: %v", err)
	}
	return sm.loginWithConnection(client)
}

func (sm *SessionManager) loginWithConnection(client *whatsmeow.Client) error {
	sm.uiHandler.PrintText("connecting..")
	if client.IsConnected() {
		client.Disconnect()
		sm.StatusChannel <- StatusMsg{false, nil}
		time.Sleep(500 * time.Millisecond)
	}

	if client.Store.ID == nil {
		if sm.Headless {
			return errors.New("not logged in, start whatscli without options to scan the QR code")
		}
		return sm.loginWithQRCode(client)
	}

	if err := client.Connect(); err != nil {
		if errors.Is(err, whatsmeow.ErrNotConnected) || errors.Is(err, whatsmeow.ErrNotLoggedIn) {
			if sm.Headless {
				return errors.New("session expired, start whatscli without options to scan the QR code again")
			}
			sm.uiHandler.PrintText("Session expired, need to scan QR code again")
			if delErr := client.Store.Delete(context.Background()); delErr != nil {
				return fmt.Errorf("failed to clear expired session: %v", delErr)
			}
			sm.client = nil
			client, err = sm.getConnection()
			if err != nil {
				return fmt.Errorf("failed to create new connection: %v", err)
			}
			return sm.loginWithQRCode(client)
		}
		return fmt.Errorf("connection failed: %v", err)
	}

	sm.uiHandler.PrintText("Session restored successfully")
	sm.StatusChannel <- StatusMsg{true, nil}
	go sm.loadRecentChats()
	return nil
}

func (sm *SessionManager) loginWithQRCode(client *whatsmeow.Client) error {
	sm.uiHandler.PrintText("Please scan the QR code with your phone")
	qrChan, err := client.GetQRChannel(context.Background())
	if err != nil {
		return fmt.Errorf("failed to initialize QR channel: %v", err)
	}
	if err = client.Connect(); err != nil {
		return fmt.Errorf("error connecting to WhatsApp: %v", err)
	}

	for evt := range qrChan {
		switch evt.Event {
		case "code":
			terminal := qrcode.New()
			terminal.SetOutput(tview.ANSIWriter(sm.uiHandler.GetWriter()))
			terminal.Get(evt.Code).Print()
		case "success":
			sm.uiHandler.PrintText("Successfully logged in!")
			if cs := sm.getChatSync(); cs != nil {
				cs.onLinked()
			}
			sm.StatusChannel <- StatusMsg{true, nil}
			go sm.loadRecentChats()
			return nil
		default:
			sm.uiHandler.PrintText("QR event: " + evt.Event)
		}
	}
	return errors.New("QR code channel closed without success")
}

func (sm *SessionManager) loadRecentChats() {
	if sm.client == nil || !sm.client.IsConnected() {
		err := errors.New("not connected to WhatsApp")
		sm.uiHandler.PrintError(err)
		signal(sm.LoginFailed, err)
		return
	}

	// Right after pairing the phone has not sent the keys for the app state yet.
	// whatsmeow syncs all of it once they arrive, see AppStateSyncComplete.
	hasAppStateKeys := sm.hasAppStateKeys()

	// Sync app state that is missing, e.g. because the cache database was deleted.
	if hasAppStateKeys {
		for _, name := range appstate.AllPatchNames {
			if name == appstate.WAPatchRegularLow {
				continue // fully synced below
			}
			sm.printAppStateError(sm.client.FetchAppState(context.Background(), name, false, true), name)
		}
	}

	addedChats := sm.addContactChats()
	sm.mergeLIDChats()
	sm.refreshContactNames()

	groups, err := sm.client.GetJoinedGroups(context.Background())
	if err == nil {
		for _, group := range groups {
			sm.db.AddChat(Chat{
				Id:      group.JID.String(),
				IsGroup: true,
				Name:    group.Name,
			})
			addedChats++
		}
	}

	sm.uiHandler.SetChats(sm.db.GetChatIds())
	if addedChats > 0 {
		sm.uiHandler.PrintText(fmt.Sprintf("Loaded %d contacts and groups", addedChats))
	}

	// The app state holds the pinned and archived state of chats, and the last
	// message time of chats that were read, marked unread or archived on another
	// device. Get what changed since the last sync; the rest is saved with the
	// chats. Syncing everything again would also undo a repair by the phone, as
	// the server keeps the patches that didn't add up, see recoverAppState.
	if hasAppStateKeys {
		sm.printAppStateError(sm.client.FetchAppState(context.Background(), appstate.WAPatchRegularLow, false, false), appstate.WAPatchRegularLow)
		sm.uiHandler.SetChats(sm.db.GetChatIds())
	}
	signal(sm.ChatsLoaded, struct{}{})
}

// hasAppStateKeys returns whether the phone has sent any keys to decrypt the app state.
func (sm *SessionManager) hasAppStateKeys() bool {
	keyID, err := sm.client.Store.AppStateKeys.GetLatestAppStateSyncKeyID(context.Background())
	return err == nil && keyID != nil
}

// printAppStateError prints an error from syncing app state, except for missing
// keys: whatsmeow requests those from the phone and syncs again when they arrive.
func (sm *SessionManager) printAppStateError(err error, name appstate.WAPatchName) {
	if err != nil && !errors.Is(err, appstate.ErrKeyNotFound) {
		sm.uiHandler.PrintError(fmt.Errorf("failed to sync %s: %v", name, err))
		sm.recoverAppState(err, name)
	}
}

// recoverAppState asks the phone for a copy of app state that failed to sync
// because it doesn't add up, a patch on the server whose hash doesn't match.
// Syncing again doesn't help then, but the phone's copy replaces it, and later
// patches work again. It is asked once per session and collection.
func (sm *SessionManager) recoverAppState(err error, name appstate.WAPatchName) bool {
	if !errors.Is(err, appstate.ErrMismatchingLTHash) && !errors.Is(err, appstate.ErrMismatchingPatchMAC) {
		return false
	}
	sm.recoveryLock.Lock()
	if sm.recoveryRequested == nil {
		sm.recoveryRequested = make(map[appstate.WAPatchName]bool)
	}
	requested := sm.recoveryRequested[name]
	sm.recoveryRequested[name] = true
	sm.recoveryLock.Unlock()
	if requested {
		return true
	}
	if _, err := sm.client.SendPeerMessage(context.Background(), whatsmeow.BuildAppStateRecoveryRequest(name)); err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to ask your phone to repair the chat settings: %v", err))
		return false
	}
	sm.uiHandler.PrintText("Asked your phone for a fresh copy of the chat settings, as the synced ones don't add up")
	return true
}

// addContactChats loads the contacts, adds a chat for every contact and returns
// how many there are.
func (sm *SessionManager) addContactChats() int {
	if sm.client == nil || sm.client.Store.Contacts == nil {
		return 0
	}
	contacts, err := sm.client.Store.Contacts.GetAllContacts(context.Background())
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to load contacts: %v", err))
		return 0
	}
	addedChats := 0
	for jid, contact := range contacts {
		name, short := contactDisplayNames(contact)
		if name == "" {
			name = jid.User
		}
		sm.db.AddContact(Contact{Id: jid.String(), Name: name, Short: short})
		if jid.Server == types.DefaultUserServer {
			sm.db.AddChat(Chat{Id: jid.String(), Name: name})
			addedChats++
		}
	}
	sm.nameOwnChat(sm.client.Store.PushName)
	return addedChats
}

// nameOwnChat names the chat with yourself like the phone does, once your name is known.
func (sm *SessionManager) nameOwnChat(pushName string) {
	if own := sm.client.Store.GetJID(); !own.IsEmpty() && pushName != "" {
		sm.db.AddChat(Chat{Id: own.ToNonAD().String(), Name: pushName + " (You)"})
	}
}

// learnLID stores that the users lid and pn are the same, and merges a chat that
// was stored under the LID. The phone sends these with the chat history, whatsmeow
// stores them too but in the background, so they may be missing when needed.
func (sm *SessionManager) learnLID(lid, pn string) {
	lidJID, lidErr := types.ParseJID(lid)
	pnJID, pnErr := types.ParseJID(pn)
	if lidErr != nil || pnErr != nil || lidJID.Server != types.HiddenUserServer || pnJID.Server != types.DefaultUserServer || sm.client == nil {
		return
	}
	lidJID, pnJID = lidJID.ToNonAD(), pnJID.ToNonAD()
	if err := sm.client.Store.LIDs.PutLIDMapping(context.Background(), lidJID, pnJID); err != nil {
		return
	}
	sm.db.MergeChat(lidJID.String(), pnJID.String())
}

// mergeLIDChats merges chats that were stored under a LID before its phone number was known.
func (sm *SessionManager) mergeLIDChats() {
	for _, chat := range sm.db.GetChatIds() {
		jid, err := types.ParseJID(chat.Id)
		if err != nil || jid.Server != types.HiddenUserServer {
			continue
		}
		if chatID := sm.chatIdForJID(jid); chatID != chat.Id {
			sm.db.MergeChat(chat.Id, chatID)
		}
	}
}

// chatIdForJID returns the chat id for jid, mapping hidden user ids (LIDs) to phone numbers.
func (sm *SessionManager) chatIdForJID(jid types.JID) string {
	if jid.Server == types.HiddenUserServer && sm.client != nil && sm.client.Store.LIDs != nil {
		if pn, err := sm.client.Store.LIDs.GetPNForLID(context.Background(), jid); err == nil && !pn.IsEmpty() {
			return pn.ToNonAD().String()
		}
	}
	return jid.ToNonAD().String()
}

// rangeTimestamp returns the last message time in seconds of an app state message range.
func rangeTimestamp(msgRange *waSyncAction.SyncActionMessageRange) int64 {
	timestamp := msgRange.GetLastMessageTimestamp()
	if timestamp > 1e12 { // milliseconds
		timestamp /= 1000
	}
	return timestamp
}

// updateChatActivity records a chat's last message time from an app state event.
func (sm *SessionManager) updateChatActivity(jid types.JID, msgRange *waSyncAction.SyncActionMessageRange, fromFullSync bool) {
	timestamp := rangeTimestamp(msgRange)
	if timestamp > 0 {
		sm.db.UpdateChatLastMessage(sm.chatIdForJID(jid), timestamp)
	}
	sm.refreshChats(fromFullSync)
}

// refreshChats shows the changed chat list after an app state event. Events of a
// full sync are followed by one refresh when it completes, see AppStateSyncComplete.
func (sm *SessionManager) refreshChats(fromFullSync bool) {
	if !fromFullSync {
		sm.uiHandler.SetChats(sm.db.GetChatIds())
	}
}

// getChatName returns the name of a group or user.
func (sm *SessionManager) getChatName(jid types.JID) string {
	if jid.Server == types.GroupServer {
		groupInfo, err := sm.client.GetGroupInfo(context.Background(), jid)
		if err == nil && groupInfo.Name != "" {
			return groupInfo.Name
		}
		return sm.db.GetIdName(jid.String())
	}
	_, name, _ := sm.contactNames(jid)
	return name
}
func (sm *SessionManager) disconnect() error {
	if sm.client != nil && sm.client.IsConnected() {
		sm.client.Disconnect()
		sm.StatusChannel <- StatusMsg{false, nil}
	}
	return nil
}

func (sm *SessionManager) logout() error {
	if sm.client == nil {
		sm.StatusChannel <- StatusMsg{false, nil}
		sm.uiHandler.PrintText("Already logged out")
		return nil
	}
	if err := sm.removeSession(true); err != nil {
		return err
	}
	sm.uiHandler.PrintText("Successfully logged out")
	return nil
}

// removeSession disconnects and removes everything that belongs to the linked
// account: the login, the cache database and the saved chats. With unlink, it
// also removes whatscli from the linked devices on the phone.
func (sm *SessionManager) removeSession(unlink bool) error {
	ctx := context.Background()
	if sm.client != nil {
		if unlink && sm.client.Store.ID != nil && sm.client.IsConnected() {
			if err := sm.client.Logout(ctx); err != nil {
				sm.uiHandler.PrintText("Warning: couldn't unlink from the phone, remove whatscli under Linked devices there: " + err.Error())
			}
		}
		sm.client.Disconnect()
		// logging out removed it already, a device that was never linked has nothing to remove
		if sm.client.Store.ID != nil {
			if err := sm.client.Store.Delete(ctx); err != nil {
				return fmt.Errorf("failed to remove login: %v", err)
			}
		}
	}
	if sm.container != nil {
		sm.container.Close()
	}
	sm.client = nil
	sm.container = nil
	sm.removeCacheStore()
	if err := sm.db.Reset(); err != nil {
		sm.uiHandler.PrintText("Warning: couldn't remove saved chats: " + err.Error())
	}
	sm.resetHistoryRequests()
	sm.currentReceiver = ""
	sm.uiHandler.SetChats(sm.db.GetChatIds())
	sm.StatusChannel <- StatusMsg{false, nil}
	return nil
}
func (sm *SessionManager) execCommand(command Command) {
	switch command.Name {
	default:
		sm.uiHandler.PrintText("[" + config.Config.Colors.Negative + "]Unknown command: [-]" + command.Name)
	case "backlog":
		sm.loadBacklog()
	case "login", "connect":
		err := sm.login()
		if err != nil {
			sm.uiHandler.PrintError(fmt.Errorf("WhatsApp connection failed: %v", err))
			sm.uiHandler.PrintText("Try using /reset to completely reset the connection")
		} else {
			sm.uiHandler.PrintText("Successfully connected to WhatsApp")
		}
	case "reset":
		sm.resetSession()
	case "disconnect":
		sm.uiHandler.PrintError(sm.disconnect())
	case "logout":
		sm.uiHandler.PrintError(sm.logout())
	case "send":
		if checkParam(command.Params, 2) {
			sm.sendText(command.Params[0], strings.Join(command.Params[1:], " "))
		} else {
			sm.printCommandUsage("send", "[chat-id[] [message text[]")
		}
	case "select":
		if checkParam(command.Params, 1) {
			sm.setCurrentReceiver(command.Params[0])
		} else {
			sm.printCommandUsage("select", "[chat-id[]")
		}
	case "read":
		sm.markCurrentChatRead()
	case "archive":
		sm.setCurrentChatArchived(true)
	case "unarchive":
		sm.setCurrentChatArchived(false)
	case "info":
		if checkParam(command.Params, 1) {
			sm.uiHandler.PrintText(sm.db.GetMessageInfo(command.Params[0]) + sm.reactionInfo(command.Params[0]))
		} else {
			sm.printCommandUsage("info", "[message-id[]")
		}
	case "download":
		sm.downloadCommand(command.Params, false)
	case "open", "show":
		sm.downloadCommand(command.Params, true)
	case "url":
		sm.openMessageURL(command.Params)
	case "upload":
		sm.sendMediaCommand(command.Params, MessageKindDocument)
	case "sendimage":
		sm.sendMediaCommand(command.Params, MessageKindImage)
	case "sendvideo":
		sm.sendMediaCommand(command.Params, MessageKindVideo)
	case "sendaudio":
		sm.sendMediaCommand(command.Params, MessageKindAudio)
	case "revoke":
		sm.revokeMessage(command.Params)
	case "react":
		sm.sendReaction(command.Params)
	case "leave":
		sm.leaveCurrentGroup()
	case "create":
		sm.createGroup(command.Params)
	case "add":
		sm.updateCurrentGroupParticipants(command.Params, whatsmeow.ParticipantChangeAdd, "add", "added new members")
	case "remove":
		sm.updateCurrentGroupParticipants(command.Params, whatsmeow.ParticipantChangeRemove, "remove", "removed members")
	case "admin":
		sm.updateCurrentGroupParticipants(command.Params, whatsmeow.ParticipantChangePromote, "admin", "promoted members")
	case "removeadmin":
		sm.updateCurrentGroupParticipants(command.Params, whatsmeow.ParticipantChangeDemote, "removeadmin", "demoted members")
	case "subject":
		sm.updateCurrentGroupSubject(command.Params)
	case "colorlist":
		out := ""
		for idx := range tcell.ColorNames {
			out += "[" + idx + "]" + idx + "[-]\n"
		}
		sm.uiHandler.PrintText(out)
	case "more":
		sm.loadBacklog()
	case "relink":
		sm.relink()
	}
}

func (sm *SessionManager) loadBacklog() {
	if sm.currentReceiver == "" {
		sm.printCommandUsage("backlog", "-> only works in a chat")
		return
	}
	if err := sm.RequestChatHistory(sm.currentReceiver); err != nil {
		sm.uiHandler.PrintError(err)
	}
}

func (sm *SessionManager) resetSession() {
	if err := sm.removeSession(false); err != nil {
		sm.uiHandler.PrintText("Warning: Couldn't remove session: " + err.Error())
	}
	dbPath := config.GetSessionFilePath() + ".db"
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		sm.uiHandler.PrintText("Warning: Couldn't remove database file: " + err.Error())
	}
	sm.uiHandler.PrintText("Session reset. Use /connect to reconnect with a new QR code.")
}
func (sm *SessionManager) markCurrentChatRead() {
	if sm.currentReceiver == "" {
		sm.printCommandUsage("read", "-> only works in a chat")
		return
	}
	count, err := sm.markChatRead(sm.currentReceiver)
	if err != nil {
		sm.uiHandler.PrintError(err)
	} else if count == 0 {
		sm.uiHandler.PrintText("No unread messages in current chat")
	}
}

// setCurrentChatArchived archives or unarchives the open chat, also on the phone.
// Like on the phone, archiving unpins the chat, and it stays archived until a
// newer message arrives, unless "keep chats archived" is enabled.
func (sm *SessionManager) setCurrentChatArchived(archive bool) {
	command, state, done := "unarchive", "not archived", "Unarchived"
	if archive {
		command, state, done = "archive", "archived", "Archived"
	}
	if sm.currentReceiver == "" {
		sm.printCommandUsage(command, "-> only works in a chat")
		return
	}
	if sm.client == nil || !sm.client.IsConnected() {
		sm.uiHandler.PrintError(errors.New("not connected to WhatsApp"))
		return
	}
	chatID := sm.currentReceiver
	for _, chat := range sm.db.GetChatIds() {
		if chat.Id == chatID && chat.InArchive == archive {
			sm.uiHandler.PrintText(sm.db.GetIdName(chatID) + " is " + state + " already")
			return
		}
	}
	target, err := sm.phoneChatJID(chatID)
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}

	// the newest message, until which the chat is archived
	var lastTime time.Time
	var lastKey *waCommon.MessageKey
	if msgs := sm.db.GetMessages(chatID); len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		lastTime = time.Unix(int64(last.Timestamp), 0)
		sender := types.EmptyJID // your own message
		if !last.FromMe {
			sender, _ = types.ParseJID(last.SenderId)
		}
		lastKey = sm.client.BuildMessageKey(target, sender, types.MessageID(last.Id))
	}
	if err = sm.sendAppState(appstate.BuildArchive(target, archive, lastTime, lastKey)); err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to %s the chat: %v", command, err))
		return
	}

	// the phone's change comes back as app state events too, show it right away
	if archive {
		archivedAt := lastTime.Unix()
		for _, chat := range sm.db.GetChatIds() {
			if chat.Id == chatID {
				archivedAt = max(archivedAt, chat.LastMessage, chat.LastIncoming)
			}
		}
		sm.db.SetChatArchived(chatID, true, archivedAt)
		sm.db.SetChatPinned(chatID, false, 0)
	} else {
		sm.db.SetChatArchived(chatID, false, 0)
	}
	sm.uiHandler.SetChats(sm.db.GetChatIds())
	sm.uiHandler.PrintText(done + " " + sm.db.GetIdName(chatID))
}

// sendAppState sends a change of chat settings to the phone. When the phone
// changed them too, the server refuses the change until whatsmeow has caught
// up, so it syncs and sends the change once more. When the synced settings
// don't add up, the phone is asked to repair them, see recoverAppState.
func (sm *SessionManager) sendAppState(patch appstate.PatchInfo) error {
	ctx := context.Background()
	repairing := errors.New("the chat settings are being repaired by your phone, try again in a moment")
	err := sm.client.SendAppState(ctx, patch)
	if err == nil {
		return nil
	} else if sm.recoverAppState(err, patch.Type) {
		return repairing
	}
	if sm.Log != nil {
		sm.Log.Warnf("Sending %s failed, syncing it and retrying: %v", patch.Type, err)
	}
	if syncErr := sm.client.FetchAppState(ctx, patch.Type, false, false); syncErr != nil {
		if sm.recoverAppState(syncErr, patch.Type) {
			return repairing
		}
		return fmt.Errorf("%v (syncing failed too: %v)", err, syncErr)
	}
	return sm.client.SendAppState(ctx, patch)
}

// markChatRead marks the unread messages of a chat as read, also on the phone,
// and returns how many there were.
func (sm *SessionManager) markChatRead(chatID string) (int, error) {
	if sm.client == nil || !sm.client.IsConnected() {
		return 0, errors.New("not connected to WhatsApp")
	}

	chatJID, err := types.ParseJID(chatID)
	if err != nil {
		return 0, fmt.Errorf("invalid JID: %v", err)
	}

	unreadMessages := sm.db.MarkChatRead(chatID)
	sm.uiHandler.SetChats(sm.db.GetChatIds())
	if len(unreadMessages) == 0 {
		return 0, nil
	}

	type senderBatch struct {
		sender    types.JID
		ids       []types.MessageID
		timestamp time.Time
	}
	batches := make(map[string]*senderBatch)
	for _, msg := range unreadMessages {
		sender := chatJID
		if strings.Contains(chatID, GROUPSUFFIX) && msg.SenderId != "" {
			sender, err = types.ParseJID(msg.SenderId)
			if err != nil {
				continue
			}
		}
		key := sender.String()
		if _, ok := batches[key]; !ok {
			batches[key] = &senderBatch{sender: sender}
		}
		batches[key].ids = append(batches[key].ids, types.MessageID(msg.Id))
		ts := time.Unix(int64(msg.Timestamp), 0)
		if ts.After(batches[key].timestamp) {
			batches[key].timestamp = ts
		}
	}

	var failed error
	for _, batch := range batches {
		if batch.timestamp.IsZero() {
			batch.timestamp = time.Now()
		}
		if err := sm.client.MarkRead(context.Background(), batch.ids, batch.timestamp, chatJID, batch.sender); err != nil {
			failed = fmt.Errorf("failed to mark messages as read: %v", err)
		}
	}
	return len(unreadMessages), failed
}

// downloadCommand downloads the attachment of a message, and opens it with open.
func (sm *SessionManager) downloadCommand(params []string, open bool) {
	if !checkParam(params, 1) {
		name := "download"
		if open {
			name = "open"
		}
		sm.printCommandUsage(name, "[message-id[]")
		return
	}

	msg, ok := sm.db.GetMessage(params[0])
	if !ok {
		sm.uiHandler.PrintError(errors.New("message not found"))
		return
	}
	path, err := sm.downloadMessage(msg, open)
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	if open {
		sm.uiHandler.OpenFile(path, openCommand(msg.Kind))
		return
	}
	sm.uiHandler.PrintText("[::d] -> " + path + "[::-]")
}

func (sm *SessionManager) openMessageURL(params []string) {
	if !checkParam(params, 1) {
		sm.printCommandUsage("url", "[message-id[]")
		return
	}
	msg, ok := sm.db.GetMessage(params[0])
	if !ok {
		sm.uiHandler.PrintError(errors.New("message not found"))
		return
	}
	url := urlPattern.FindString(msg.Text)
	if url == "" {
		sm.uiHandler.PrintText("No URL found in message")
		return
	}
	sm.uiHandler.OpenFile(url, "")
}

// openCommand returns the configured command that opens attachments of a kind,
// or "" for the default app
func openCommand(kind MessageKind) string {
	switch kind {
	case MessageKindImage:
		return config.Config.General.ImageCommand
	case MessageKindVideo:
		return config.Config.General.VideoCommand
	case MessageKindAudio:
		return config.Config.General.AudioCommand
	case MessageKindDocument:
		return config.Config.General.DocumentCommand
	}
	return ""
}

func (sm *SessionManager) sendMediaCommand(params []string, kind MessageKind) {
	if sm.currentReceiver == "" {
		sm.printCommandUsage(commandNameForKind(kind), "-> only works in a chat")
		return
	}
	if !checkParam(params, 1) {
		sm.printCommandUsage(commandNameForKind(kind), "/path/to/file")
		return
	}
	path := strings.Join(params, " ")
	sm.uiHandler.PrintError(sm.sendMedia(sm.currentReceiver, path, kind))
}

func (sm *SessionManager) revokeMessage(params []string) {
	if !checkParam(params, 1) {
		sm.printCommandUsage("revoke", "[message-id[]")
		return
	}
	if sm.client == nil || !sm.client.IsConnected() {
		sm.uiHandler.PrintError(errors.New("not connected to WhatsApp"))
		return
	}

	msg, ok := sm.db.GetMessage(params[0])
	if !ok {
		sm.uiHandler.PrintError(errors.New("message not found"))
		return
	}
	chatJID, err := types.ParseJID(msg.ChatId)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("invalid chat JID: %v", err))
		return
	}
	if _, err = sm.client.RevokeMessage(context.Background(), chatJID, types.MessageID(msg.Id)); err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	sm.db.MarkMessageRevoked(msg.Id)
	if sm.currentReceiver == msg.ChatId {
		sm.uiHandler.NewScreen(sm.getMessages(msg.ChatId))
	}
	sm.uiHandler.PrintText("revoked: " + msg.Id)
}

// sendReaction reacts to the message with the id in params[0] with the emoji in
// params[1], or removes the reaction without one
func (sm *SessionManager) sendReaction(params []string) {
	if !checkParam(params, 1) {
		sm.printCommandUsage("react", "[message-id[] [emoji[]")
		return
	}
	if sm.client == nil || !sm.client.IsConnected() {
		sm.uiHandler.PrintError(errors.New("not connected to WhatsApp"))
		return
	}
	msg, ok := sm.db.GetMessage(params[0])
	if !ok {
		sm.uiHandler.PrintError(errors.New("message not found"))
		return
	}
	chatJID, err := types.ParseJID(msg.ChatId)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("invalid chat JID: %v", err))
		return
	}
	// an empty sender means a message of your own
	sender := types.EmptyJID
	if !msg.FromMe {
		if sender, err = types.ParseJID(msg.SenderId); err != nil || sender.IsEmpty() {
			sender = chatJID
		}
	}
	reaction := strings.Join(params[1:], " ")
	if _, err = sm.client.SendMessage(context.Background(), chatJID, sm.client.BuildReaction(chatJID, sender, types.MessageID(msg.Id), reaction)); err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to send reaction: %v", err))
		return
	}
	if chatID, ok := sm.db.SetReaction(msg.Id, ReactorMe, reaction, time.Now().Unix()); ok && chatID == sm.currentReceiver {
		sm.uiHandler.NewScreen(sm.getMessages(chatID))
	}
}

// reactionInfo lists who reacted to a message with what, for the message info
func (sm *SessionManager) reactionInfo(messageID string) string {
	msg, ok := sm.db.GetMessage(messageID)
	if !ok || len(msg.Reactions) == 0 {
		return ""
	}
	reactions := sm.namedReactions(msg)
	for idx, reaction := range reactions {
		reactions[idx] = "  " + tview.Escape(reaction)
	}
	return "\nReactions:\n" + strings.Join(reactions, "\n")
}

// namedReactions returns the reactions to a message as the emoji and the name
// of who reacted, sorted
func (sm *SessionManager) namedReactions(msg Message) []string {
	reactions := make([]string, 0, len(msg.Reactions))
	for reactor, reaction := range msg.Reactions {
		name := "You"
		if reactor != ReactorMe {
			name = reactor
			if jid, err := types.ParseJID(reactor); err == nil {
				_, name, _ = sm.contactNames(jid)
			}
		}
		reactions = append(reactions, reaction+" "+name)
	}
	sort.Strings(reactions)
	return reactions
}

// ShortName returns the short name to show for a user, "You" for yourself.
func (sm *SessionManager) ShortName(id string) string {
	if id == ReactorMe {
		return "You"
	}
	if jid, err := types.ParseJID(id); err == nil {
		_, _, short := sm.contactNames(jid)
		return short
	}
	return id
}

// reactorID returns the key of a reaction in Message.Reactions
func (sm *SessionManager) reactorID(sender types.JID, fromMe bool) string {
	if fromMe {
		return ReactorMe
	}
	return sm.chatIdForJID(sender)
}

// reactorFromKey returns who reacted from the key of a reaction in the chat history
func (sm *SessionManager) reactorFromKey(key *waCommon.MessageKey, chat types.JID) string {
	if key.GetFromMe() {
		return ReactorMe
	}
	if participant, err := types.ParseJID(key.GetParticipant()); err == nil && !participant.IsEmpty() {
		return sm.chatIdForJID(participant)
	}
	return sm.chatIdForJID(chat)
}

func (sm *SessionManager) leaveCurrentGroup() {
	groupJID, err := sm.currentGroupJID()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	if err = sm.client.LeaveGroup(context.Background(), groupJID); err != nil {
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

	groupInfo, err := sm.client.CreateGroup(context.Background(), whatsmeow.ReqCreateGroup{
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
	groupJID, err := sm.currentGroupJID()
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

	if _, err = sm.client.UpdateGroupParticipants(context.Background(), groupJID, participants, action); err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	sm.uiHandler.PrintText(success + " for " + groupJID.String())
}

func (sm *SessionManager) updateCurrentGroupSubject(params []string) {
	groupJID, err := sm.currentGroupJID()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	if !checkParam(params, 1) {
		sm.printCommandUsage("subject", "new-subject -> in group chat")
		return
	}

	name := strings.Join(params, " ")
	if err = sm.client.SetGroupName(context.Background(), groupJID, name); err != nil {
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

func (sm *SessionManager) currentGroupJID() (types.JID, error) {
	if sm.currentReceiver == "" || !strings.Contains(sm.currentReceiver, GROUPSUFFIX) {
		return types.JID{}, errors.New("not a group")
	}
	return types.ParseJID(sm.currentReceiver)
}

func (sm *SessionManager) printCommandUsage(command, usage string) {
	sm.uiHandler.PrintText("[" + config.Config.Colors.Negative + "]Usage:[-] " + command + " " + usage)
}

func checkParam(arr []string, length int) bool {
	return arr != nil && len(arr) >= length
}

func (sm *SessionManager) getMessages(wid string) []Message {
	return sm.db.GetMessages(wid)
}

func (sm *SessionManager) sendText(wid, text string) {
	if sm.client == nil || !sm.client.IsConnected() {
		sm.uiHandler.PrintError(errors.New("not connected to WhatsApp"))
		return
	}

	receiver, err := types.ParseJID(wid)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("invalid JID: %v", err))
		return
	}

	raw := &waProto.Message{Conversation: proto.String(text)}
	sm.lastSent = time.Now()
	resp, err := sm.client.SendMessage(context.Background(), receiver, raw)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to send message: %v", err))
		return
	}

	newMsg := sm.outgoingMessageFromSendResponse(resp, wid, raw, MessageKindText, text, "", "")
	sm.messageSent(newMsg)
}

// messageSent shows a message that was sent, and marks its chat as read if configured.
func (sm *SessionManager) messageSent(msg Message) {
	sm.db.AddMessage(msg, false)
	if sm.currentReceiver == msg.ChatId {
		sm.uiHandler.NewMessage(msg)
	}
	sm.uiHandler.SetChats(sm.db.GetChatIds())
	if config.Config.General.MarkReadOnSend {
		if _, err := sm.markChatRead(msg.ChatId); err != nil {
			sm.uiHandler.PrintError(err)
		}
	}
}

func (sm *SessionManager) sendMedia(chatID, path string, kind MessageKind) error {
	if sm.client == nil || !sm.client.IsConnected() {
		return errors.New("not connected to WhatsApp")
	}

	data, mimeType, fileName, err := readUploadFile(path)
	if err != nil {
		return err
	}

	receiver, err := types.ParseJID(chatID)
	if err != nil {
		return fmt.Errorf("invalid JID: %v", err)
	}

	uploadResp, err := sm.client.Upload(context.Background(), data, uploadMediaType(kind))
	if err != nil {
		return fmt.Errorf("failed to upload file: %v", err)
	}

	fileLength := uploadResp.FileLength
	raw := &waProto.Message{}
	switch kind {
	case MessageKindImage:
		raw.ImageMessage = &waProto.ImageMessage{
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &fileLength,
		}
	case MessageKindVideo:
		raw.VideoMessage = &waProto.VideoMessage{
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &fileLength,
		}
	case MessageKindAudio:
		raw.AudioMessage = &waProto.AudioMessage{
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &fileLength,
			PTT:           proto.Bool(false),
		}
	case MessageKindDocument:
		raw.DocumentMessage = &waProto.DocumentMessage{
			Mimetype:      proto.String(mimeType),
			Title:         proto.String(fileName),
			FileName:      proto.String(fileName),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &fileLength,
		}
	default:
		return errors.New("unsupported media type")
	}

	sm.lastSent = time.Now()
	resp, err := sm.client.SendMessage(context.Background(), receiver, raw)
	if err != nil {
		return fmt.Errorf("failed to send media message: %v", err)
	}

	text := mediaDisplayText(kind, fileName, "")
	newMsg := sm.outgoingMessageFromSendResponse(resp, chatID, raw, kind, text, mimeType, fileName)
	sm.messageSent(newMsg)
	return nil
}

func (sm *SessionManager) outgoingMessageFromSendResponse(resp whatsmeow.SendResponse, chatID string, raw *waProto.Message, kind MessageKind, text, mimeType, fileName string) Message {
	selfID := ""
	if sm.client != nil && sm.client.Store != nil && sm.client.Store.ID != nil {
		selfID = sm.client.Store.ID.String()
	}

	contactID := chatID
	if strings.Contains(chatID, GROUPSUFFIX) {
		contactID = selfID
	}

	return Message{
		Id:           string(resp.ID),
		ChatId:       chatID,
		SenderId:     selfID,
		ContactId:    contactID,
		ContactName:  sm.db.GetIdName(contactID),
		ContactShort: sm.db.GetIdShort(contactID),
		Timestamp:    uint64(resp.Timestamp.Unix()),
		FromMe:       true,
		Text:         text,
		Kind:         kind,
		MimeType:     mimeType,
		FileName:     fileName,
		RawMessage:   raw,
	}
}

// notificationText returns the title and text of the notification for a new
// message: messages in groups are titled with the group and name the sender
func notificationText(msg Message, chatName string) (string, string) {
	if strings.Contains(msg.ChatId, GROUPSUFFIX) {
		return chatName, msg.ContactShort + ": " + msg.Text
	}
	return msg.ContactShort, msg.Text
}

// notify shows a desktop notification with an icon, the app's if empty, see
// desktopNotify, or rings the terminal bell
func notify(title, message, icon string) error {
	if !config.Config.General.EnableNotifications {
		return nil
	} else if config.Config.General.UseTerminalBell {
		_, err := fmt.Printf("\a")
		return err
	}
	return desktopNotify(title, message, icon)
}

type eventHandler struct {
	sm *SessionManager
}

func (eh *eventHandler) Handle(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		eh.handleLiveMessage(v)
	case *events.HistorySync:
		eh.handleHistorySync(v)
	case *events.OfflineSyncCompleted:
		signal(eh.sm.OfflineSynced, struct{}{})
	case *events.AppStateSyncComplete:
		// full syncs don't refresh the chat list per event, see refreshChats
		if v.Name == appstate.WAPatchCriticalUnblockLow { // the contact list
			eh.sm.addContactChats()
			eh.sm.refreshContactNames()
		}
		eh.sm.uiHandler.SetChats(eh.sm.db.GetChatIds())
		if v.Recovery {
			eh.sm.uiHandler.PrintText("Chat settings repaired")
		}
		if cs := eh.sm.getChatSync(); cs != nil {
			cs.onAppStateSynced(v.Name)
		}
	case *events.DeleteChat:
		eh.sm.logAppState("delete", v.JID, v.Timestamp, v.FromFullSync, "", v.Action.GetMessageRange())
		deletedAt := rangeTimestamp(v.Action.GetMessageRange())
		if deletedAt <= 0 {
			deletedAt = v.Timestamp.Unix()
		}
		eh.sm.db.SetChatDeleted(eh.sm.chatIdForJID(v.JID), deletedAt)
		eh.sm.refreshChats(v.FromFullSync)
	case *events.PairSuccess:
		// pairing sets up the device store, move the cached parts again
		if err := eh.sm.useCacheStore(eh.sm.client.Store); err != nil {
			eh.sm.uiHandler.PrintError(err)
		}
	case *events.MarkChatAsRead:
		eh.sm.logAppState("read", v.JID, v.Timestamp, v.FromFullSync, fmt.Sprintf("read=%v", v.Action.GetRead()), v.Action.GetMessageRange())
		eh.sm.updateChatActivity(v.JID, v.Action.GetMessageRange(), v.FromFullSync)
	case *events.Archive:
		eh.sm.logAppState("archive", v.JID, v.Timestamp, v.FromFullSync, fmt.Sprintf("archived=%v", v.Action.GetArchived()), v.Action.GetMessageRange())
		eh.sm.db.SetChatArchived(eh.sm.chatIdForJID(v.JID), v.Action.GetArchived(), rangeTimestamp(v.Action.GetMessageRange()))
		eh.sm.updateChatActivity(v.JID, v.Action.GetMessageRange(), v.FromFullSync)
	case *events.UnarchiveChatsSetting:
		eh.sm.logAppState("unarchive setting", types.EmptyJID, v.Timestamp, v.FromFullSync, fmt.Sprintf("unarchive=%v", v.Action.GetUnarchiveChats()), nil)
		eh.sm.db.SetKeepArchived(!v.Action.GetUnarchiveChats())
		eh.sm.refreshChats(v.FromFullSync)
	case *events.PushNameSetting:
		eh.sm.nameOwnChat(v.Action.GetName())
		eh.sm.uiHandler.SetChats(eh.sm.db.GetChatIds())
	case *events.Pin:
		eh.sm.logAppState("pin", v.JID, v.Timestamp, v.FromFullSync, fmt.Sprintf("pinned=%v", v.Action.GetPinned()), nil)
		eh.sm.db.SetChatPinned(eh.sm.chatIdForJID(v.JID), v.Action.GetPinned(), v.Timestamp.Unix())
		eh.sm.refreshChats(v.FromFullSync)
	case *events.Connected:
		eh.sm.StatusChannel <- StatusMsg{true, nil}
		eh.sm.connection.connected()
	case *events.Disconnected:
		eh.sm.StatusChannel <- StatusMsg{false, nil}
		eh.sm.connection.disconnected()
	case *events.KeepAliveTimeout:
		eh.sm.connection.unresponsive()
	case *events.KeepAliveRestored:
		eh.sm.connection.connected()
	case *events.LoggedOut:
		eh.sm.StatusChannel <- StatusMsg{false, nil}
		eh.sm.uiHandler.PrintText("Logged out: " + fmt.Sprintf("%v", v.Reason))
		eh.sm.connection.loggedOut(fmt.Sprintf("%v", v.Reason))
	}
}

func (eh *eventHandler) handleLiveMessage(evt *events.Message) {
	msg, action, ok := eh.normalizeEventMessage(evt)
	if !ok {
		return
	}

	switch action {
	case "react":
		if chatID, ok := eh.sm.db.SetReaction(msg.Id, msg.SenderId, msg.Text, int64(msg.Timestamp)); ok && chatID == eh.sm.currentReceiver {
			eh.sm.uiHandler.NewScreen(eh.sm.getMessages(chatID))
		}
		return
	case "revoke":
		if eh.sm.db.MarkMessageRevoked(msg.Id) && eh.sm.currentReceiver == msg.ChatId {
			eh.sm.uiHandler.NewScreen(eh.sm.getMessages(msg.ChatId))
		}
		eh.sm.uiHandler.SetChats(eh.sm.db.GetChatIds())
		return
	case "ignore":
		return
	}

	markUnread := !msg.FromMe && msg.ChatId != eh.sm.currentReceiver
	isNew := eh.sm.db.AddMessage(msg, markUnread)
	if msg.ChatId == eh.sm.currentReceiver {
		if isNew {
			eh.sm.uiHandler.NewMessage(msg)
		} else {
			eh.sm.uiHandler.NewScreen(eh.sm.getMessages(msg.ChatId))
		}
	} else if markUnread && msg.Timestamp > uint64(time.Now().Unix()-30) {
		// showing it can take a moment, e.g. on Windows, which starts PowerShell
		title, text := notificationText(msg, eh.sm.db.GetIdName(msg.ChatId))
		go func() {
			if err := sendNotification(title, text, eh.sm.chatPicture(msg.ChatId)); err != nil {
				eh.sm.uiHandler.PrintError(err)
			}
		}()
	}
	eh.sm.uiHandler.SetChats(eh.sm.db.GetChatIds())
}

func (eh *eventHandler) handleHistorySync(evt *events.HistorySync) {
	if evt == nil || evt.Data == nil {
		return
	}

	for _, mapping := range evt.Data.GetPhoneNumberToLidMappings() {
		eh.sm.learnLID(mapping.GetLidJID(), mapping.GetPnJID())
	}

	var chatIDs []string
	messageCount := 0
	for _, conv := range evt.Data.GetConversations() {
		// a conversation has either a LID with its phone number, or the other way around
		eh.sm.learnLID(conv.GetID(), conv.GetPnJID())
		eh.sm.learnLID(conv.GetLidJID(), conv.GetID())
		chatID := conv.GetID()
		if chatID == "" {
			chatID = conv.GetNewJID()
		}
		if chatID == "" {
			continue
		}

		chatJID, err := types.ParseJID(chatID)
		if err != nil {
			continue
		}
		// store one-to-one chats under the phone number, like the contact list
		chatID = eh.sm.chatIdForJID(chatJID)
		chatIDs = append(chatIDs, chatID)

		chatName := conv.GetName()
		if chatName == "" {
			chatName = conv.GetDisplayName()
		}
		if chatName == "" {
			if mappedJID, err := types.ParseJID(chatID); err == nil {
				chatName = eh.sm.getChatName(mappedJID)
			}
		}
		if strings.Contains(chatName, "@") {
			chatName = "" // no name found, keep the one from the contacts
		}

		lastMessage := int64(conv.GetLastMsgTimestamp())
		if lastMessage == 0 {
			lastMessage = int64(conv.GetConversationTimestamp())
		}
		eh.sm.db.AddChat(Chat{
			Id:          chatID,
			IsGroup:     chatJID.Server == types.GroupServer,
			Name:        chatName,
			Unread:      int(conv.GetUnreadCount()),
			LastMessage: lastMessage,
		})

		for _, histMsg := range conv.GetMessages() {
			webMsg := histMsg.GetMessage()
			if webMsg == nil {
				continue
			}
			parsed, err := eh.sm.client.ParseWebMessage(chatJID, webMsg)
			if err != nil {
				continue
			}
			msg, action, ok := eh.normalizeEventMessage(parsed)
			if ok && action == "react" {
				eh.sm.db.SetReaction(msg.Id, msg.SenderId, msg.Text, int64(msg.Timestamp))
				continue
			}
			if !ok || action != "" {
				continue
			}
			// the LID of the chat may not be mapped to its phone number yet
			msg.ChatId = chatID
			eh.sm.db.AddMessage(msg, false)
			messageCount++
			for _, reaction := range webMsg.GetReactions() {
				eh.sm.db.SetReaction(msg.Id, eh.sm.reactorFromKey(reaction.GetKey(), chatJID), reaction.GetText(), reaction.GetSenderTimestampMS()/1000)
			}
		}
		eh.sm.db.UpdateChatUnread(chatID, int(conv.GetUnreadCount()))

		// The phone knows whether the chat is archived, which the app state alone
		// doesn't tell, see GetChatIds. A conversation can be sent in several parts,
		// some without messages and time, which say nothing about it.
		if lastMessage > 0 {
			if conv.GetArchived() {
				eh.sm.db.SetChatArchived(chatID, true, lastMessage)
			} else {
				eh.sm.db.SetChatUnarchived(chatID, lastMessage)
			}
		}
		if eh.sm.Log != nil {
			eh.sm.Log.Debugf("History conversation %s: archived=%v pinned=%v last message %s, %d messages",
				chatID, conv.GetArchived(), conv.GetPinned() > 0, formatTimestamp(lastMessage), len(conv.GetMessages()))
		}
	}

	eh.sm.uiHandler.SetChats(eh.sm.db.GetChatIds())
	if eh.sm.currentReceiver != "" {
		eh.sm.uiHandler.NewScreen(eh.sm.getMessages(eh.sm.currentReceiver))
	}
	if cs := eh.sm.getChatSync(); cs != nil {
		cs.onHistory(evt.Data, chatIDs, messageCount)
	}
	if evt.Data.GetSyncType() == waHistorySync.HistorySync_ON_DEMAND {
		// an answer without messages doesn't say for which chat
		if len(chatIDs) == 0 {
			eh.sm.finishAllHistoryRequests()
		}
		for _, chatID := range chatIDs {
			eh.sm.finishHistoryRequest(chatID)
		}
		sent := 0
		for _, conv := range evt.Data.GetConversations() {
			sent += len(conv.GetMessages())
		}
		if sent == 0 {
			eh.sm.uiHandler.PrintText("Your phone has no older messages")
		}
	}
}

func (eh *eventHandler) normalizeEventMessage(evt *events.Message) (Message, string, bool) {
	if evt == nil || evt.Message == nil {
		return Message{}, "ignore", false
	}

	// a reaction carries the message it reacts to in Id, the emoji in Text and who reacted in SenderId
	if reaction := evt.Message.GetReactionMessage(); reaction != nil {
		return Message{
			Id:        reaction.GetKey().GetID(),
			ChatId:    eh.sm.chatIdForJID(evt.Info.Chat),
			Text:      reaction.GetText(),
			SenderId:  eh.sm.reactorID(evt.Info.Sender, evt.Info.IsFromMe),
			Timestamp: uint64(evt.Info.Timestamp.Unix()),
		}, "react", true
	}

	if protocol := evt.Message.GetProtocolMessage(); protocol != nil {
		if protocol.GetType() == waProto.ProtocolMessage_REVOKE && protocol.GetKey() != nil {
			return Message{
				Id:     protocol.GetKey().GetID(),
				ChatId: eh.sm.chatIdForJID(evt.Info.Chat),
			}, "revoke", true
		}
		return Message{}, "ignore", false
	}

	msg, ok := eh.messageFromInfo(evt.Info, evt.Message)
	return msg, "", ok
}

func (eh *eventHandler) messageFromInfo(info types.MessageInfo, raw *waProto.Message) (Message, bool) {
	if raw == nil {
		return Message{}, false
	}

	if info.Chat.IsEmpty() {
		return Message{}, false
	}
	// store one-to-one chats under the phone number, like the contact list
	chatID := eh.sm.chatIdForJID(info.Chat)

	contactID, contactName, contactShort := eh.contactForMessage(info)
	msg := Message{
		Id:           string(info.ID),
		ChatId:       chatID,
		SenderId:     info.Sender.String(),
		ContactId:    contactID,
		ContactName:  contactName,
		ContactShort: contactShort,
		Timestamp:    uint64(info.Timestamp.Unix()),
		FromMe:       info.IsFromMe,
		RawMessage:   raw,
	}

	switch {
	case raw.GetConversation() != "":
		msg.Kind = MessageKindText
		msg.Text = raw.GetConversation()
		return msg, true
	case raw.GetExtendedTextMessage() != nil:
		ext := raw.GetExtendedTextMessage()
		msg.Kind = MessageKindText
		msg.Text = ext.GetText()
		msg.Forwarded = ext.GetContextInfo().GetIsForwarded()
		return msg, true
	case raw.GetImageMessage() != nil:
		image := raw.GetImageMessage()
		msg.Kind = MessageKindImage
		msg.MimeType = image.GetMimetype()
		msg.Text = mediaDisplayText(MessageKindImage, "", image.GetCaption())
		msg.Forwarded = image.GetContextInfo().GetIsForwarded()
		return msg, true
	case raw.GetVideoMessage() != nil:
		video := raw.GetVideoMessage()
		msg.Kind = MessageKindVideo
		msg.MimeType = video.GetMimetype()
		msg.Text = mediaDisplayText(MessageKindVideo, "", video.GetCaption())
		msg.Forwarded = video.GetContextInfo().GetIsForwarded()
		return msg, true
	case raw.GetAudioMessage() != nil:
		audio := raw.GetAudioMessage()
		msg.Kind = MessageKindAudio
		msg.MimeType = audio.GetMimetype()
		msg.Text = mediaDisplayText(MessageKindAudio, "", "")
		msg.Forwarded = audio.GetContextInfo().GetIsForwarded()
		return msg, true
	case raw.GetDocumentMessage() != nil:
		doc := raw.GetDocumentMessage()
		msg.Kind = MessageKindDocument
		msg.MimeType = doc.GetMimetype()
		msg.FileName = doc.GetFileName()
		msg.Text = mediaDisplayText(MessageKindDocument, doc.GetFileName(), doc.GetCaption())
		msg.Forwarded = doc.GetContextInfo().GetIsForwarded()
		return msg, true
	default:
		return Message{}, false
	}
}

func (eh *eventHandler) contactForMessage(info types.MessageInfo) (string, string, string) {
	if info.IsGroup {
		return eh.sm.contactNames(info.Sender)
	}
	return eh.sm.contactNames(info.Chat)
}

// contactNames returns the id, name and short name to show for a user. Like the
// phone, it prefers the name saved in the contacts over the user's profile name.
func (sm *SessionManager) contactNames(jid types.JID) (string, string, string) {
	jid = jid.ToNonAD()
	// contacts are stored under the phone number, groups address users by LID
	jids := []types.JID{jid}
	if pn, err := types.ParseJID(sm.chatIdForJID(jid)); err == nil && pn != jid {
		jids = []types.JID{pn, jid}
	}
	id := jids[0].String()
	if sm.client != nil && sm.client.Store.Contacts != nil {
		for _, lookup := range jids {
			contact, err := sm.client.Store.Contacts.GetContact(context.Background(), lookup)
			if err != nil || !contact.Found {
				continue
			}
			if name, short := contactDisplayNames(contact); name != "" {
				return id, name, short
			}
		}
	}
	return id, sm.db.GetIdName(id), sm.db.GetIdShort(id)
}

// refreshContactNames updates the sender names of the messages in memory, which
// may have been saved or received before the contacts were known.
func (sm *SessionManager) refreshContactNames() {
	sm.db.UpdateContactNames(func(contactID string) (string, string, string, bool) {
		jid, err := types.ParseJID(contactID)
		if err != nil {
			return "", "", "", false
		}
		id, name, short := sm.contactNames(jid)
		return id, name, short, true
	})
}

// contactDisplayNames returns the name and short name to show for a contact.
// Like the phone, it prefers the name saved in the contacts over the profile name.
func contactDisplayNames(contact types.ContactInfo) (string, string) {
	name := firstNonEmpty(contact.FullName, contact.FirstName, contact.PushName, contact.BusinessName)
	short := firstNonEmpty(contact.FirstName, contact.FullName, contact.PushName, contact.BusinessName)
	return name, short
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (sm *SessionManager) downloadMessage(msg Message, preview bool) (string, error) {
	if sm.client == nil || !sm.client.IsConnected() {
		return "", errors.New("not connected to WhatsApp")
	}

	downloadable, err := downloadableFromMessage(msg)
	if err != nil {
		return "", err
	}

	baseDir := config.Config.General.DownloadPath
	if preview {
		baseDir = config.Config.General.PreviewPath
	}
	if err = os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}

	fileName := downloadFileName(msg)
	fullPath := filepath.Join(baseDir, fileName)
	if _, err = os.Stat(fullPath); err == nil {
		return fullPath, nil
	}

	data, err := sm.client.Download(context.Background(), downloadable)
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(fullPath, data, 0o644); err != nil {
		return "", err
	}
	return fullPath, nil
}

func downloadableFromMessage(msg Message) (whatsmeow.DownloadableMessage, error) {
	if msg.RawMessage == nil {
		return nil, errors.New("This is not a downloadable message")
	}
	switch msg.Kind {
	case MessageKindImage:
		if media := msg.RawMessage.GetImageMessage(); media != nil {
			return media, nil
		}
	case MessageKindVideo:
		if media := msg.RawMessage.GetVideoMessage(); media != nil {
			return media, nil
		}
	case MessageKindAudio:
		if media := msg.RawMessage.GetAudioMessage(); media != nil {
			return media, nil
		}
	case MessageKindDocument:
		if media := msg.RawMessage.GetDocumentMessage(); media != nil {
			return media, nil
		}
	}
	return nil, errors.New("This is not a downloadable message")
}

func readUploadFile(path string) ([]byte, string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", "", err
	}
	fileName := filepath.Base(path)
	mimeType := detectMimeType(path, data)
	return data, mimeType, fileName, nil
}

func detectMimeType(path string, data []byte) string {
	if len(data) == 0 {
		if extType := mime.TypeByExtension(filepath.Ext(path)); extType != "" {
			return stripMimeParams(extType)
		}
		return "application/octet-stream"
	}
	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}
	detected := stripMimeParams(http.DetectContentType(sample))
	if extType := mime.TypeByExtension(filepath.Ext(path)); extType != "" {
		extType = stripMimeParams(extType)
		if detected == "application/octet-stream" || strings.HasPrefix(extType, "audio/") || strings.HasPrefix(extType, "video/") {
			return extType
		}
	}
	return detected
}

func stripMimeParams(value string) string {
	if idx := strings.Index(value, ";"); idx >= 0 {
		return value[:idx]
	}
	return value
}

func downloadFileName(msg Message) string {
	if msg.FileName != "" {
		safeName := path.Base(strings.ReplaceAll(msg.FileName, "\\", "/"))
		if safeName != "" && safeName != "." && safeName != ".." {
			return safeName
		}
	}
	ext := ""
	if msg.MimeType != "" {
		if exts, err := mime.ExtensionsByType(msg.MimeType); err == nil && len(exts) > 0 {
			ext = exts[0]
		}
	}
	return msg.Id + ext
}

func uploadMediaType(kind MessageKind) whatsmeow.MediaType {
	switch kind {
	case MessageKindImage:
		return whatsmeow.MediaImage
	case MessageKindVideo:
		return whatsmeow.MediaVideo
	case MessageKindAudio:
		return whatsmeow.MediaAudio
	default:
		return whatsmeow.MediaDocument
	}
}

func commandNameForKind(kind MessageKind) string {
	switch kind {
	case MessageKindImage:
		return "sendimage"
	case MessageKindVideo:
		return "sendvideo"
	case MessageKindAudio:
		return "sendaudio"
	default:
		return "upload"
	}
}

func mediaDisplayText(kind MessageKind, fileName, caption string) string {
	label := "[FILE]"
	switch kind {
	case MessageKindImage:
		label = "[IMAGE]"
	case MessageKindVideo:
		label = "[VIDEO]"
	case MessageKindAudio:
		label = "[AUDIO]"
	case MessageKindDocument:
		label = "[DOCUMENT]"
	}
	parts := []string{label}
	if fileName != "" && kind == MessageKindDocument {
		parts = append(parts, fileName)
	}
	if caption != "" {
		parts = append(parts, caption)
	}
	return strings.Join(parts, " ")
}
