//go:build !windows

package notify

import "github.com/gen2brain/beeep"

// Desktop shows a desktop notification with an icon, the default if empty.
func Desktop(title, message, icon string) error {
	return beeep.Notify(title, message, icon)
}
