//go:build !windows && !linux

package ai

import "os/exec"

// startServerProcess starts llama-server, which is ended when whatscli closes,
// see Server.Stop
func startServerProcess(cmd *exec.Cmd) error {
	return cmd.Start()
}

// hideWindow does nothing, programs have no windows of their own here
func hideWindow(*exec.Cmd) {}
