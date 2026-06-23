//go:build !windows

package mcp

import (
	"os/exec"
	"syscall"
)

// setPgid 将子进程放入独立进程组（pgid == child pid）。
// 配合 killProcessGroup 可一次性终止 npx 及其所有后代进程（如 node）。
func setPgid(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup 向以 pid 为 pgid 的整个进程组发送 SIGKILL。
// 要求 setPgid 已在 Start() 时被调用（pgid == 启动进程的 PID）。
func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
