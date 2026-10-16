package model

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lang315/sshgate/app/hubapi"
)

// FilterHosts matches name, host or user, case-insensitively; the port
// and key path are not searched (renderer filterHosts).
func FilterHosts(servers []hubapi.Server, q string) []hubapi.Server {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return servers
	}
	var out []hubapi.Server
	for _, s := range servers {
		for _, f := range []string{s.Name, s.Host, s.User} {
			if strings.Contains(strings.ToLower(f), q) {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// PasswordChecks counts characters as the renderer does (the hub's own
// check is 8 bytes, which any 8 characters satisfy).
func PasswordChecks(pw, again string) (length, match bool) {
	return utf8.RuneCountInString(pw) >= 8, again != "" && pw == again
}

func Addr(s hubapi.Server) string { return fmt.Sprintf("%s@%s:%d", s.User, s.Host, s.Port) }

func Tile(name string) string {
	r, size := utf8.DecodeRuneInString(name)
	if size == 0 {
		return ""
	}
	return string(unicode.ToUpper(r))
}
