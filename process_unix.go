//go:build !windows

package main

import (
	"os/exec"
)

// 非 Windows 平台的空实现
func setProcessGroupID(cmd *exec.Cmd) {
	// Unix/Mac 不需要特殊处理
}
