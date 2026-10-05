//go:build windows

package messages

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-toast/toast"
	"github.com/normen/whatscli/config"
	"golang.org/x/sys/windows/registry"
)

// notificationAppID identifies whatscli to Windows, which shows its name and
// icon on its notifications, see registerNotificationApp
const notificationAppID = "whatscli"

var registerNotificationOnce sync.Once

// notificationIcon is the path of the icon on the notifications
var notificationIcon string

// registerNotificationApp registers the name and icon of whatscli with Windows
// for the current user. Without it, notifications show as from PowerShell,
// which Windows uses to show them.
func registerNotificationApp() {
	if configPath := config.GetConfigFilePath(); configPath != "" {
		// named after its checksum, as Windows keeps showing an icon it knows by its path
		iconPath := filepath.Join(filepath.Dir(configPath), fmt.Sprintf("whatscli-%08x.png", crc32.ChecksumIEEE(NotificationIcon)))
		if len(NotificationIcon) > 0 && os.WriteFile(iconPath, NotificationIcon, 0644) == nil {
			notificationIcon = iconPath
			// of earlier versions of the icon
			if old, err := filepath.Glob(filepath.Join(filepath.Dir(configPath), "whatscli-*.png")); err == nil {
				for _, file := range old {
					if file != iconPath {
						os.Remove(file)
					}
				}
			}
		}
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+notificationAppID, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer key.Close()
	key.SetStringValue("DisplayName", appName)
	if notificationIcon != "" {
		key.SetStringValue("IconUri", notificationIcon)
	}
}

// desktopNotify shows a toast notification with an icon, the app's if empty.
func desktopNotify(title, message, icon string) error {
	registerNotificationOnce.Do(registerNotificationApp)
	if icon == "" {
		icon = notificationIcon
	}
	notification := toast.Notification{AppID: notificationAppID, Title: title, Message: message, Icon: icon}
	return notification.Push()
}
