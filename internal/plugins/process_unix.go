//go:build !windows

package plugins

import (
 "os/exec"
 "syscall"
)

// The server handles terminal signals and asks each plugin to shut down via
// its protocol. Sharing the terminal process group would kill plugins first.
func isolatePluginSignals(cmd *exec.Cmd) {cmd.SysProcAttr=&syscall.SysProcAttr{Setpgid:true}}
