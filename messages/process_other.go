//go:build !windows && !linux

package messages

import "os/exec"

// startServerProcess starts llama-server, which is ended when whatscli closes,
// see stopModel
func startServerProcess(cmd *exec.Cmd) error {
	return cmd.Start()
}

// hideWindow does nothing, programs have no windows of their own here
func hideWindow(*exec.Cmd) {}
