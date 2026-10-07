//go:build linux

package ai

import (
	"os/exec"
	"syscall"
)

// startServerProcess starts llama-server, which the system ends when whatscli
// ends, also when whatscli is killed
func startServerProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return cmd.Start()
}

// hideWindow does nothing, programs have no windows of their own here
func hideWindow(*exec.Cmd) {}
