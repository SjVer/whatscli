> **NOTICE:** This fork makes a lot of large additions and changes to normen/whatscli, almost entirely vibe-coded. Note that I am well aware of how concerning that would be if I intended to PR this or otherwise cared about the quality of the codebase. I do not. This is a utility I use myself, and you're welcome to use it too.

# whatscli

A command line interface for WhatsApp, based on [go-whatsmeow](https://github.com/tulir/whatsmeow) and [tview](https://github.com/rivo/tview)

![whatscli-screenshot](/doc/screenshot.png?raw=true "WhatsCLI 0.6.5")

## What this fork adds

Compared to [normen/whatscli](https://github.com/normen/whatscli):

**Chat list**
- Matches the phone: pinned chats first, archived chats in a collapsible "Archived" folder, chats stored under a LID merged with the phone number chat, and a pin icon for pinned chats
- Archive or unarchive the selected chat with `a`, also on the phone
- `/relink` links whatscli again to get the full chat history from the phone
- Chats with a draft are marked with ✎, and each chat keeps its own draft of the typed message
- Unread counts follow the phone: chats read on the phone or another device are read in whatscli too

**Messages**
- WhatsApp formatting: `*bold*`, `_italic_`, `~strikethrough~` and `` `code` ``
- Emoji reactions, shown below each message and as a dim "... reacted ..." line; react to the selected message with `e`
- Replies, shown above the message they reply to; reply to the selected message with `a`
- ✓ sent, ✓✓ delivered and coloured ✓✓ read ticks, also per member in groups (see the message info)
- Short timestamps, and messages of one sender grouped below one header
- Mentions: type `@` in a group to mention a member, and mentions are shown by name and highlighted
- People who aren't in your contacts are shown by their profile name, in italics, or their phone number
- Stickers are shown and can be opened
- Older messages are loaded from the phone the first time a chat is opened, and with `Ctrl+b`
- Status notices, like loading errors, are dim lines below the chat they are about

**Input**
- A text area that grows with the message, up to 8 lines: `Enter` sends, `Shift+Enter` or `Alt+Enter` starts a new line
- `Ctrl+Backspace`, `Ctrl+Delete` and `Ctrl+Left/Right` work by words, and pasted text with line breaks is sent as one message
- Emoji with `:shortcodes:` and suggestions, recently used ones first
- Paste an image from the clipboard with `Alt+v`, sent with the typed message as its caption
- Commands are colored, and `/search` searches the chat list or the loaded messages of a chat

**AI model, optional and running on your own computer**
- Alt texts for images, stickers, videos, GIFs and animated stickers, like `[IMAGE: a dog on a beach]`
- `/recap 10m` sums up a chat, `/ask 5d what did we plan for sunday?` answers a question about it
- See [Alt texts and recaps by a local AI model](#alt-texts-and-recaps-by-a-local-ai-model)

**Other**
- Notifications on Windows come from whatscli with its own icon, show the picture of the chat, and also tell when the connection is lost or back
- Link with a code instead of the QR code with `/code` and your phone number
- Attachments open in the background with the default app or a command per type (`image_command`, `video_command`, ...)
- The status bar shows how long ago data was last received, and the terminal title the number of new messages
- `-dump`, `-dump-chat` and `-log` print what whatscli knows, for debugging
- Many fixes for freezes, crashes and sync problems, and the code is split into files by concern

## Features

Things that work.

- Sending and receiving WhatsApp messages in a command line app
- Connects through the Web App API without a browser
- Uses QR code for simple setup
- Allows downloading and opening image/video/audio/document attachments
- Allows sending images, video, audio and documents
- Allows color customization
- Allows basic group management
- Supports desktop notifications
- Binaries for Windows, Mac, Linux and RaspBerry Pi

### Caveats

Heres some things you might expect to work that don't. Plus some other things I should mention.

- Message history depends on what WhatsApp syncs to companion devices and may require `/backlog`
- No automation of messages, no sending of messages through shell commands
- Meta obviously doesn't endorse or like these kinds of apps and they're likely to break when WhatsApp changes stuff in their web app

## Similar Apps

Similar but more features:
- [Nchat](https://github.com/d99kris/nchat)

## Installation

How to get it running and how to use it

### Latest Release

Always fresh, always up to date.

- Download a release
- Put the binary in your PATH (optional)
- Run with `whatscli` (or double-click)
- Scan the QR code with WhatsApp on your phone (resize shell or change font size to see whole code)

### Package Managers

Some ways to install via package managers are supported but the installed version might be out of date.

#### MacOS (homebrew)

- `brew install normen/tap/whatscli`

#### Arch Linux (AUR)

- `https://aur.archlinux.org/packages/whatscli/`

## Usage

Most information, all commands and key bindings are availabe through the in-app help, simply type `/help` and/or `/commands`.

### Login

When starting up, whatscli will immediately try to connect to the WhatsApp server to log in. Keep your phone ready to scan the appearing QR code in WhatsApp on your Phone. If you don't manage to scan the code quick enough just restart the application. If you can not see the whole QR code, reduce the font size of your terminal or increase the window size.

After scanning the QR code the chats should be populated. After you have done this once, whatscli will be able to log into WhatsApp automatically on start. To log out of WhapsApp completely type `/logout`.

### Messaging / Commands

Select a chat on the left and start typing in the input field at the bottom to send messages. Switch between the chat list and the input fiel with `<Tab>`.

For issuing commands the same input field is used. By default commands are prefixed with `/`. You can for example use the `/sendimage /path/to/file.jpg` command to send images, see `/help` for more commands.

When paths are given for commands you don't need to surround the path in quotes, even if it contains spaces. Also don't prefix spaces with backslashes (as the copy-paste function of MacOS does for example).

### Messages

When pressing `Ctrl-w` (default mapping) you enter "message selection mode" which allows selecting a single message and performing operations on them. For example pressing `o` while a message is selected opens its attachment with the default app of your system.

#### Copy-Pasting User IDs

Some commands such as the `/add` and `/remove` require a "user id" as their input. You can copy the user ID of a selected chat or a selected message to the clipboard with `Ctrl-c` (default mapping) and easily append them to the current input using `Ctrl-v`.

### Alt texts and recaps by a local AI model

whatscli can describe the images, stickers, videos and GIFs of the chat you open in a few words, like `[IMAGE: a dog running on a beach]`, with a vision model that runs on your own computer. Add one line to the `[general]` section of `whatscli.config`:

```
ai_model = ggml-org/gemma-3-4b-it-GGUF
```

whatscli downloads [llama.cpp](https://github.com/ggml-org/llama.cpp) and the model the first time (about 3 GB for this one) next to the config, and runs it while whatscli runs. On a computer without a good graphics card, `ggml-org/SmolVLM-500M-Instruct-GGUF` is much smaller. Videos and GIFs are described by 2 to 8 of their frames, more for longer ones, which are taken with [ffmpeg](https://ffmpeg.org): the one you installed, or on Windows one that whatscli downloads (about 80 MB). The media of older messages expire on the WhatsApp server, so mostly recent ones get an alt text.

The same model sums up chats: `/recap 10m` recaps the messages of the open chat of the last 10 minutes, below them, and takes times like `2h`, `1h30m`, `1d` or `3 days`. Without a time it recaps the unread messages, or the last 50. `/ask 5d what did we plan for sunday?` answers a question from the messages of the last 5 days, or from all loaded ones without a time. (`alt_text_model`, the earlier name of the setting, still works.)

### Notifications

The app supports desktop notifications, to enable it set `enable_notifications = true` in `whatscli.config`. They show the picture of the chat, and also tell when the connection is lost or back. On Windows they are sent as whatscli with its icon, elsewhere through the `gen2brain/beeep` library. Set `use_terminal_bell = true` to ring your terminal's bell instead of sending a desktop notification.

### Configuration

Most key bindings, colors and other options can be configured in the `whatscli.config` file, the `/help` command shows its location.

## Development

This app started as my first attempt at writing something in go. Some areas that are marked with `TODO` can still be improved but work mostly. If you want to contribute features or improve the code thats great, send a PR and we can discuss.

### Building

Using a recent version of go, building should be straightforward. Either use `go build`, `go run` etc. or use the included Makefile.

### Structure Overview

The UI is in the files of the main package, around a tview app running on the main routine that `main.go` sets up: `chatlist.go`, `render.go`, `input.go`, `keys.go` (the keymap, based on the tslocum/cbind library) and so on. It manages the selection of messages in the current chat as well as displaying the messages and chat list that the session manager sends.

The `messages/session_manager.go` (with the other files in `messages`, split by concern) runs a separate go routine to receive messages from the `go-whatsmeow` library which in turn runs the websocket connection to the WhatsApp server. The session manager receives the messages from `go-whatsmeow` and the commands from the UI via channels that it drains on its main routine. It then updates the UI accordingly using the `UiMessageHandler` interface. This ensures "thread safe" management of the connection and data while both UI and network connection run separately.

Session manager is designed "object like", the MessageDatabase in `messages/storage.go` is similar and somewhat linked to the session manager. In theory the session manager could be run multiple times (multiple accounts) or a different implementation of a session manager could connect to a different service like e.g. Telegram.

In `messages/messages.go` most interfaces and data structures for communication are kept.

The `config/settings.go` keeps a singleton `Config` struct with the config that is loaded via the gopkg.in/ini.v1 library when the app starts. This makes it easy to quickly add new configuration items with default values that can be used across the app.

## License

This software is released under MIT license. Remember that this gives you all freedom except for slapping your name on it.
