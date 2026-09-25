//go:build !windows

package messages

import "github.com/gen2brain/beeep"

// desktopNotify shows a desktop notification with an icon, the default if empty.
func desktopNotify(title, message, icon string) error {
	return beeep.Notify(title, message, icon)
}
