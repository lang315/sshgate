//go:build unix

package hubtest

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"time"
)

// MCPSocket is the hub's MCP door, where the bridge sends AI requests.
func MCPSocket(runtimeDir string) string { return filepath.Join(runtimeDir, "sshgate", "hub.sock") }

type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
}

// Exec runs one MCP-door exec, as the bridge does for an AI's command, on
// its own connection; the result arrives once the hub decides and runs it.
func Exec(sock string, params map[string]any) <-chan ExecResult {
	out := make(chan ExecResult, 1)
	go func() {
		c, err := net.Dial("unix", sock)
		if err != nil {
			out <- ExecResult{Err: err}
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(30 * time.Second)) // longer than any approval wait in the tests
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "exec", "params": params})
		if _, err := c.Write(append(b, '\n')); err != nil {
			out <- ExecResult{Err: err}
			return
		}
		line, err := bufio.NewReader(c).ReadBytes('\n')
		if err != nil {
			out <- ExecResult{Err: err}
			return
		}
		var m struct {
			Result *struct {
				ExitCode int    `json:"exitCode"`
				Stdout   string `json:"stdout"`
				Stderr   string `json:"stderr"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(line, &m); err != nil {
			out <- ExecResult{Err: err}
			return
		}
		if m.Error != nil {
			out <- ExecResult{Err: errors.New(m.Error.Message)}
			return
		}
		if m.Result == nil {
			out <- ExecResult{Err: errors.New("exec: reply has neither result nor error")}
			return
		}
		out <- ExecResult{ExitCode: m.Result.ExitCode, Stdout: m.Result.Stdout, Stderr: m.Result.Stderr}
	}()
	return out
}
