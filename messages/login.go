package messages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/qrcode"
	"github.com/rivo/tview"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func (sm *SessionManager) getConnection() (*whatsmeow.Client, error) {
	if sm.client() == nil {
		dbPath := config.GetSessionFilePath() + ".db"
		container, err := sqlstore.New(context.Background(), "sqlite3", "file:"+dbPath+"?_foreign_keys=on"+sqliteOptions, waLog.Noop)
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
		sm.clientPtr.Store(client)
		sm.container = container
	}
	return sm.client(), nil
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
		upgraded, err := sqlstore.New(context.Background(), "sqlite3", cachePath+"?_foreign_keys=on"+sqliteOptions, waLog.Noop)
		if err != nil {
			return fmt.Errorf("failed to open cache database: %v", err)
		}
		upgraded.Close()
		db, err := sql.Open("sqlite3", cachePath+"?_foreign_keys=off"+sqliteOptions)
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

// sqliteOptions make writing to the databases faster: whatsmeow writes the keys
// of each message received, which takes long after being offline for a while
// when every write waits for the disk. With a write-ahead log, a crash of the
// computer can lose the last writes, but doesn't damage the database.
const sqliteOptions = "&_journal_mode=WAL&_synchronous=NORMAL"

// removeDatabase deletes an SQLite database with its write-ahead log.
func removeDatabase(path string) error {
	for _, file := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// closeClient disconnects the client and closes its database, so that a new
// one can be made, see getConnection: two would both be connected as whatscli.
func (sm *SessionManager) closeClient() {
	client := sm.client()
	if client != nil {
		client.RemoveEventHandlers()
		client.Disconnect()
		sm.clientPtr.Store(nil)
	}
	if sm.container != nil {
		sm.container.Close()
		sm.container = nil
	}
}

// removeCacheStore closes and deletes the cache database.
func (sm *SessionManager) removeCacheStore() {
	if sm.cacheContainer != nil {
		sm.cacheContainer.Close()
		sm.cacheContainer = nil
	}
	if err := removeDatabase(config.GetSessionFilePath() + ".cache.db"); err != nil {
		sm.uiHandler.PrintText("Warning: Couldn't remove cache database: " + tview.Escape(err.Error()))
	}
}

func (sm *SessionManager) login() error {
	sm.closeClient()
	client, err := sm.getConnection()
	if err != nil {
		return fmt.Errorf("failed to create WhatsApp connection: %v", err)
	}
	return sm.loginWithConnection(client)
}

func (sm *SessionManager) loginWithConnection(client *whatsmeow.Client) error {
	sm.uiHandler.SetNotice("", connectionNotice, "Connecting...")
	if client.IsConnected() {
		client.Disconnect()
		sm.sendStatus()
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
			sm.closeClient()
			client, err = sm.getConnection()
			if err != nil {
				return fmt.Errorf("failed to create new connection: %v", err)
			}
			return sm.loginWithQRCode(client)
		}
		return fmt.Errorf("connection failed: %v", err)
	}

	sm.uiHandler.SetNotice("", connectionNotice, "Session restored, connecting...")
	sm.sendStatus()
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
	sm.linking.Store(true)
	defer sm.linking.Store(false)

	for evt := range qrChan {
		switch evt.Event {
		case "code":
			terminal := qrcode.New()
			terminal.SetOutput(tview.ANSIWriter(sm.uiHandler.GetWriter()))
			terminal.Get(evt.Code).Print()
			// below the code, which can be bigger than the screen; each new code is printed below the last
			sm.uiHandler.PrintText(linkCodeHint())
		case "success":
			sm.uiHandler.PrintText("Successfully logged in!")
			if cs := sm.getChatSync(); cs != nil {
				cs.onLinked()
			}
			sm.sendStatus()
			go sm.loadRecentChats()
			return nil
		default:
			sm.uiHandler.PrintText("QR event: " + tview.Escape(evt.Event))
		}
	}
	return errors.New("QR code channel closed without success")
}

func (sm *SessionManager) disconnect() error {
	client := sm.client()
	if client != nil && client.IsConnected() {
		client.Disconnect()
		sm.sendStatus()
	}
	return nil
}

func (sm *SessionManager) logout() error {
	if sm.client() == nil {
		sm.sendStatus()
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
	if client := sm.client(); client != nil {
		if unlink && client.Store.ID != nil && client.IsConnected() {
			if err := client.Logout(ctx); err != nil {
				sm.uiHandler.PrintText("Warning: couldn't unlink from the phone, remove whatscli under Linked devices there: " + tview.Escape(err.Error()))
			}
		}
		client.Disconnect()
		// logging out removed it already, a device that was never linked has nothing to remove
		if client.Store.ID != nil {
			if err := client.Store.Delete(ctx); err != nil {
				return fmt.Errorf("failed to remove login: %v", err)
			}
		}
	}
	sm.closeClient()
	sm.removeCacheStore()
	if err := sm.db.Reset(); err != nil {
		sm.uiHandler.PrintText("Warning: couldn't remove saved chats: " + tview.Escape(err.Error()))
	}
	sm.resetHistoryRequests()
	// of the account that was logged out
	sm.forgetMembers("")
	sm.pictures.forget()
	sm.receiverLock.Lock()
	sm.currentReceiver = ""
	sm.receiverLock.Unlock()
	sm.uiHandler.SetChats(sm.db.GetChatIds())
	sm.sendStatus()
	return nil
}

func (sm *SessionManager) resetSession() {
	if err := sm.removeSession(false); err != nil {
		sm.uiHandler.PrintText("Warning: Couldn't remove session: " + tview.Escape(err.Error()))
	}
	if err := removeDatabase(config.GetSessionFilePath() + ".db"); err != nil {
		sm.uiHandler.PrintText("Warning: Couldn't remove database file: " + tview.Escape(err.Error()))
	}
	sm.uiHandler.PrintText("Session reset. Use /connect to reconnect with a new QR code.")
}
