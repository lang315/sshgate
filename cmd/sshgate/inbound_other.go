//go:build !unix

package main

import (
	"errors"
	"os"
)

func execProc(string, []string, []string) error {
	return errors.New("sshgate inbound is not supported on this platform")
}

func lockAudit(*os.File) error { return nil }
