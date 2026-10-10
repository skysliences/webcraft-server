//go:build windows

package plugins

import "os/exec"

func isolatePluginSignals(cmd *exec.Cmd) {}
