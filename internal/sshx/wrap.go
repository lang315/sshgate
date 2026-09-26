package sshx

import "github.com/lang315/sshgate/internal/config"

func WrapSudoNoPassword(cmd string) string {
	return "sudo -n sh -c '" + config.EscapeShellSingleQuote(cmd) + "'"
}

func WrapSudoWithPassword(cmd string) string {
	inner := "exec " + cmd + " </dev/null"
	return "sudo -k -S -p '' sh -c '" + config.EscapeShellSingleQuote(inner) + "'"
}

func FrameSuCommand(cmd, nonce string) string {
	return cmd + "; echo " + nonce + ":$?"
}
