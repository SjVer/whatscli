package messages

import (
	"fmt"

	"github.com/normen/whatscli/config"
)

// appName is the name of whatscli on its notifications
const appName = "whatscli"

// NotificationIcon is the PNG icon of the app on notifications, set by main
var NotificationIcon []byte

// sendNotification shows a notification, see notify
var sendNotification = notify

// notificationText returns the title and text of the notification for a new
// message: messages in groups are titled with the group and name the sender
func notificationText(msg Message, chatName string) (string, string) {
	if isGroupID(msg.ChatId) {
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
