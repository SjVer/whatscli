package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/notify"
	"github.com/normen/whatscli/ui"
)

// the icon of whatscli on notifications
//
//go:embed icon.png
var notificationIcon []byte

var VERSION string = "v2.0.0"

func main() {
	dump := flag.Bool("dump", false, "print the chat list and unread messages instead of starting the UI")
	dumpWait := flag.Duration("dump-wait", 15*time.Second, "how long -dump waits for messages received while offline")
	dumpChat := flag.String("dump-chat", "", "with -dump, load this chat from the phone and print its messages")
	logPath := flag.String("log", "", "write the WhatsApp connection log to this file")
	flag.Parse()

	config.InitConfig()
	notify.Icon = notificationIcon
	logger, err := openLog(*logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if *dump {
		os.Exit(runDump(*dumpWait, *dumpChat, logger))
	}
	ui.Run(VERSION, logger)
}
