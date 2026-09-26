//go:build !unix

package sshconfig

import "os/exec"

func killGroup(*exec.Cmd) {}
