//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// Windows 特定的进程组设置，确保 taskkill /T 能正确终止子进程
func setProcessGroupID(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000200, // CREATE_NEW_PROCESS_GROUP
	}
}
