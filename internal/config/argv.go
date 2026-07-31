package config

import (
	"fmt"
	"strconv"
	"strings"
)

type CLIConfig struct {
	Host, User, Password, Key string
	SuPassword, SudoPassword  string
	Port, TimeoutMs, MaxChars int
	DisableSudo               bool
	HasHost                   bool
	HasSuPassword             bool
	HasSudoPassword           bool
}

func ParseArgv(args []string) map[string]*string {
	out := map[string]*string{}
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			continue
		}
		body := a[2:]
		if i := strings.IndexByte(body, '='); i >= 0 {
			v := body[i+1:]
			out[body[:i]] = &v
		} else {
			out[body] = nil
		}
	}
	return out
}

func ParseMaxChars(raw *string) int {
	if raw == nil {
		return 1000
	}
	if strings.EqualFold(*raw, "none") {
		return -1
	}
	n, err := strconv.Atoi(*raw)
	if err != nil {
		return 1000
	}
	if n <= 0 {
		return -1
	}
	return n
}

func BuildCLIConfig(m map[string]*string) (CLIConfig, error) {
	get := func(k string) string {
		if v, ok := m[k]; ok && v != nil {
			return *v
		}
		return ""
	}
	c := CLIConfig{
		Host: get("host"), User: get("user"), Password: get("password"),
		Key: get("key"), SuPassword: get("suPassword"), SudoPassword: get("sudoPassword"),
		Port: 22, TimeoutMs: 60000, MaxChars: ParseMaxChars(m["maxChars"]),
	}
	_, c.HasHost = m["host"]
	_, c.HasSuPassword = m["suPassword"]
	_, c.HasSudoPassword = m["sudoPassword"]
	_, c.DisableSudo = m["disableSudo"]

	if p := m["port"]; p != nil {
		n, err := strconv.Atoi(*p)
		if err != nil {
			return c, fmt.Errorf("Invalid --port")
		}
		c.Port = n
	}
	if t := m["timeout"]; t != nil {
		if n, err := strconv.Atoi(*t); err == nil {
			c.TimeoutMs = n
		}
	}

	var errs []string
	if c.Host == "" {
		errs = append(errs, "Missing required --host")
	}
	if c.User == "" {
		errs = append(errs, "Missing required --user")
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("Configuration error:\n%s", strings.Join(errs, "\n"))
	}
	return c, nil
}
