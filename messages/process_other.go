//go:build !windows && !linux

package messages

import "os/exec"

// startServerProcess starts llama-server, which is ended when whatscli closes,
// see stopServer
func startServerProcess(cmd *exec.Cmd) error {
	return cmd.Start()
}
