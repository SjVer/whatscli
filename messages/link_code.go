package messages

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"unicode"

	"github.com/normen/whatscli/config"
	"go.mau.fi/whatsmeow"
)

// Linking with a code: instead of scanning the QR code, the phone number is
// typed with /code, and the phone links whatscli with the code it gives.

// linkCodeHint is shown with the QR code
func linkCodeHint() string {
	return "Or link with a code: type " + config.Config.General.CmdPrefix + "code and your phone number with its country code, e.g. " +
		config.Config.General.CmdPrefix + "code +31 6 12345678"
}

// LinkWithCode asks WhatsApp for a code to link whatscli with the phone of the
// number, while the QR code is shown. It is called from the UI, not the session
// manager, which waits for the phone to link meanwhile: the client is set
// before linking starts, see the linking flag.
func (sm *SessionManager) LinkWithCode(phone string) {
	client := sm.client
	if !sm.linking.Load() || client == nil {
		sm.uiHandler.PrintText("Linking with a code works while the QR code is shown, e.g. after " + config.Config.General.CmdPrefix + "relink")
		return
	}
	number := strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1 // spaces, + and dashes
	}, phone)
	if len(number) < 7 {
		sm.uiHandler.PrintText(linkCodeHint())
		return
	}
	go func() {
		code, err := client.PairPhone(context.Background(), number, true, whatsmeow.PairClientChrome, linkClientName())
		if err != nil {
			sm.uiHandler.PrintError(fmt.Errorf("failed to get a code to link with: %v", err))
			return
		}
		sm.uiHandler.PrintText("On your phone, open the notification from WhatsApp, or WhatsApp > Settings > Linked devices > Link a device > Link with phone number instead, and enter: [::b]" + code + "[::-]")
	}()
}

// linkClientName is how whatscli is shown on the phone when linking with a
// code: WhatsApp only allows common browsers and systems
func linkClientName() string {
	switch runtime.GOOS {
	case "windows":
		return "Chrome (Windows)"
	case "darwin":
		return "Chrome (Mac OS)"
	}
	return "Chrome (Linux)"
}
