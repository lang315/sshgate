//go:build !unix && !windows

package sshconfig

import "os/exec"

func runTree(cmd *exec.Cmd) ([]byte, error) { return cmd.Output() }
