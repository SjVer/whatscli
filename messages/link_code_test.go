package messages

import (
	"strings"
	"testing"
)

// printedUi keeps the printed text
type printedUi struct {
	recordingUi
	printed []string
}

func (u *printedUi) PrintText(text string) { u.printed = append(u.printed, text) }

func TestLinkingWithACodeOnlyWhileLinking(t *testing.T) {
	ui := &printedUi{}
	sm := &SessionManager{uiHandler: ui}
	sm.LinkWithCode("+31 6 12345678")
	if len(ui.printed) != 1 || !strings.Contains(ui.printed[0], "while the QR code is shown") {
		t.Errorf("expected to be told when it works, got %q", ui.printed)
	}
}
