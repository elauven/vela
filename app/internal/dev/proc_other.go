//go:build !unix

package dev

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

func stopProcess(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}

func killProcess(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}
