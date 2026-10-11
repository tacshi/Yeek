//go:build !windows

package engine

import "os/exec"

func hideConsole(*exec.Cmd) {}
