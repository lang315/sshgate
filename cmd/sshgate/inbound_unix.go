//go:build unix

package main

import (
	"os"
	"syscall"
)

func execProc(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }

// lockAudit is an exclusive flock on the audit file, the lock the hub's append takes.
func lockAudit(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }
