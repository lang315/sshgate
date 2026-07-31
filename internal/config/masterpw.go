package config

import (
	"fmt"
	"os"
	"strings"
)

func ReadMasterPasswordFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("master password file %s must be mode 0600 (no group/other access)", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(raw), "\r\n"), nil
}

func ResolveMasterPassword(env func(string) string) (string, bool, error) {
	p := env("SSH_MCP_MASTER_PASSWORD_FILE")
	if p == "" {
		return "", false, nil
	}
	pw, err := ReadMasterPasswordFile(p)
	if err != nil {
		return "", false, err
	}
	return pw, true, nil
}
