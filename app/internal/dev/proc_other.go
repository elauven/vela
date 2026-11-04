//go:build !unix

package dev

import (
	"os/exec"
	"strconv"
)

func setProcessGroup(cmd *exec.Cmd) {}

// stopProcess stops the process and its children: Kill alone would leave
// the server that go run started, which keeps the port and the output
// pipe open.
func stopProcess(cmd *exec.Cmd) {
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}

func killProcess(cmd *exec.Cmd) {
	stopProcess(cmd)
}
