//go:build unix

package main

import "syscall"

func execProc(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }
