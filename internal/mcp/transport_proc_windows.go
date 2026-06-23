//go:build windows

package mcp

import (
	"errors"
	"os/exec"
)

// setPgid 在 Windows 上为空操作（不支持 POSIX 进程组）。
func setPgid(_ *exec.Cmd) {}

// killProcessGroup 在 Windows 上不支持进程组 kill，返回错误以触发单进程回退。
func killProcessGroup(_ int) error {
	return errors.New("process group kill not supported on Windows")
}
