package messages

import (
	"fmt"

	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/notify"
)

// sendNotification shows a notification, see showNotification
var sendNotification = showNotification

// notificationText returns the title and text of the notification for a new
// message: messages in groups are titled with the group and name the sender
func notificationText(msg Message, chatName string) (string, string) {
	if isGroupID(msg.ChatId) {
		return chatName, msg.ContactShort + ": " + msg.Text
	}
	return msg.ContactShort, msg.Text
}

// showNotification shows a desktop notification with an icon, the app's if
// empty, see notify.Desktop, or rings the terminal bell
func showNotification(title, message, icon string) error {
	if !config.Config.General.EnableNotifications {
		return nil
	} else if config.Config.General.UseTerminalBell {
		_, err := fmt.Printf("\a")
		return err
	}
	return notify.Desktop(title, message, icon)
}
