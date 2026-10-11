//go:build !windows

package whois

import "os/exec"

func detach(cmd *exec.Cmd) {}
