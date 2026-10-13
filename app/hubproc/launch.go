// Package hubproc runs sshgate hub as a subprocess and keeps it running.
package hubproc

import (
	"path/filepath"
	"strings"
)

// Launch is how to start the hub.
type Launch struct {
	Command string
	Args    []string
	Env     []string
}

// LaunchFor picks the hub's binary, arguments and environment, as
// desktop/src/main/hubProcess.ts's hubLaunch does. SSHGATE_* are dev and
// test knobs: a packaged app ignores them and strips them from the hub's
// environment, because whoever sets the environment of the installed app
// could otherwise swap the hub (which receives the master password),
// point it at another vault, or stretch the idle lock.
func LaunchFor(env []string, packaged bool, resources, repoRoot, goos string, exists func(string) bool) Launch {
	knobs := map[string]string{}
	var out []string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "SSHGATE_") {
			if packaged {
				continue
			}
			knobs[k] = v
		}
		out = append(out, kv)
	}
	exe := "sshgate"
	if goos == "windows" {
		exe = "sshgate.exe"
	}
	local := filepath.Join(repoRoot, exe)
	if packaged {
		local = filepath.Join(resources, exe)
	}
	args := []string{"hub"}
	if v := knobs["SSHGATE_STORE"]; v != "" {
		args = append(args, "--store="+v)
	}
	if v := knobs["SSHGATE_SSH_CONFIG"]; v != "" {
		args = append(args, "--sshConfig="+v)
	}
	if v := knobs["SSHGATE_IDLE_LOCK"]; v != "" {
		args = append(args, "--idleLock="+v)
	}
	cmd := knobs["SSHGATE_BIN"]
	if cmd == "" {
		cmd = exe
		if exists(local) {
			cmd = local
		}
	}
	return Launch{Command: cmd, Args: args, Env: out}
}
