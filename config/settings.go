package config

import (
	"fmt"
	"os"
	"os/user"

	"github.com/adrg/xdg"
	"gopkg.in/ini.v1"
)

var configFilePath string

type IniFile struct {
	*General
	*Keymap
	*Ui
	*Colors
}

type General struct {
	DownloadPath string
	// where opened attachments are downloaded, a folder in the temporary folder if empty
	PreviewPath         string
	CmdPrefix           string
	EnableNotifications bool
	// a Hugging Face repo of a vision model that describes images and stickers,
	// like ggml-org/gemma-3-4b-it-GGUF, which whatscli runs itself; off if empty
	AltTextModel       string
	UseTerminalBell    bool
	BacklogMsgQuantity int
	// typing :name: gives emoji, with suggestions
	EmojiShortcodes bool
	// sending a message to a chat marks it as read
	MarkReadOnSend bool
}

type Keymap struct {
	SwitchPanels    string
	FocusMessages   string
	FocusInput      string
	FocusChats      string
	Copyuser        string
	Pasteuser       string
	CommandBacklog  string
	CommandRead     string
	CommandConnect  string
	CommandQuit     string
	CommandHelp     string
	MessageDownload string
	MessageOpen     string
	MessageUrl      string
	MessageInfo     string
	MessageRevoke   string
	MessageReact    string
	MessageReply    string
	// archives or unarchives the selected chat in the chat list
	ChatArchive string
	// attaches the image on the clipboard to the typed message
	PasteImage string
}

type Ui struct {
	ChatSidebarWidth int
	// how many lines the messages scroll with the mouse wheel and Up/Down, and with PgUp/PgDn
	ScrollLines int
	PageLines   int
}

type Colors struct {
	Background        string
	Text              string
	ForwardedText     string
	ListHeader        string
	ListContact       string
	ListGroup         string
	ChatContact       string
	ChatMe            string
	Borders           string
	InputBackground   string
	InputText         string
	SilentCommandText string
	CommandText       string
	UnreadCount       string
	Mention           string
	// the label of images and other media, like [IMAGE: a dog]
	MediaLabel string
	ReadMarker string
	Positive   string
	Negative   string
}

var Config = IniFile{
	&General{
		DownloadPath:        GetHomeDir() + "Downloads",
		PreviewPath:         "", // a folder in the temporary folder, see previewDir
		CmdPrefix:           "/",
		EnableNotifications: false,
		UseTerminalBell:     false,
		BacklogMsgQuantity:  10,
		EmojiShortcodes:     true,
		MarkReadOnSend:      false,
	},
	&Keymap{
		SwitchPanels:    "Tab",
		FocusMessages:   "Ctrl+w",
		FocusInput:      "Ctrl+Space",
		FocusChats:      "Ctrl+e",
		CommandBacklog:  "Ctrl+b",
		CommandRead:     "Ctrl+n",
		Copyuser:        "Ctrl+c",
		Pasteuser:       "Ctrl+v",
		CommandConnect:  "Ctrl+r",
		CommandQuit:     "Ctrl+q",
		CommandHelp:     "Ctrl+?",
		MessageDownload: "d",
		MessageInfo:     "i",
		MessageOpen:     "o",
		MessageUrl:      "u",
		MessageRevoke:   "r",
		MessageReact:    "e",
		MessageReply:    "a",
		ChatArchive:     "a",
		PasteImage:      "Alt+v",
	},
	&Ui{
		ChatSidebarWidth: 30,
		ScrollLines:      1,
		PageLines:        10,
	},
	&Colors{
		Background:        "black",
		Text:              "white",
		ForwardedText:     "purple",
		ListHeader:        "yellow",
		ListContact:       "green",
		ListGroup:         "blue",
		ChatContact:       "green",
		ChatMe:            "blue",
		Borders:           "white",
		InputBackground:   "blue",
		InputText:         "white",
		SilentCommandText: "violet",
		CommandText:       "blue",
		UnreadCount:       "yellow",
		Mention:           "dodgerblue",
		MediaLabel:        "teal",
		ReadMarker:        "deepskyblue",
		Positive:          "green",
		Negative:          "red",
	},
}

func InitConfig() {
	var err error
	if configFilePath, err = xdg.ConfigFile("whatscli/whatscli.config"); err == nil {
		// add any new values
		var cfg *ini.File
		if cfg, err = ini.Load(configFilePath); err == nil {
			cfg.NameMapper = ini.TitleUnderscore
			cfg.ValueMapper = os.ExpandEnv
			if section, err := cfg.GetSection("general"); err == nil {
				section.MapTo(&Config.General)
			}
			if section, err := cfg.GetSection("keymap"); err == nil {
				section.MapTo(&Config.Keymap)
			}
			if section, err := cfg.GetSection("ui"); err == nil {
				section.MapTo(&Config.Ui)
			}
			if section, err := cfg.GetSection("colors"); err == nil {
				section.MapTo(&Config.Colors)
			}
		} else {
			cfg = ini.Empty()
			cfg.NameMapper = ini.TitleUnderscore
			cfg.ValueMapper = os.ExpandEnv
			if err = ini.ReflectFromWithMapper(cfg, &Config, ini.TitleUnderscore); err == nil {
				err = cfg.SaveTo(configFilePath)
			}
		}
	}
	if err != nil {
		fmt.Print(err.Error())
	}
}

func GetConfigFilePath() string {
	return configFilePath
}

func GetSessionFilePath() string {
	if sessionFilePath, err := xdg.ConfigFile("whatscli/session"); err == nil {
		return sessionFilePath
	}
	return GetHomeDir() + ".whatscli.session"
}

// gets the OS home dir with a path separator at the end
func GetHomeDir() string {
	if usr, err := user.Current(); err == nil {
		return usr.HomeDir + string(os.PathSeparator)
	} else if home, err := os.UserHomeDir(); err == nil {
		return home + string(os.PathSeparator)
	}
	return ""
}
