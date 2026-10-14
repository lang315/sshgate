package model

import (
	"fmt"
	"strconv"

	"github.com/lang315/sshgate/app/hubapi"
)

func TrayTitle(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func TrayTooltip(n int) string {
	if n == 0 {
		return "sshgate"
	}
	return fmt.Sprintf("sshgate: %d request%s waiting", n, plural(n))
}

// NotificationText never includes the description or stdin.
func NotificationText(r hubapi.Request) (title, body string) {
	title = "AI wants to run a command"
	if r.Sudo {
		title = "AI wants to run a command with sudo"
	}
	full := []rune(r.Server + ": " + r.Command)
	if len(full) > 120 {
		return title, string(full[:120]) + "…"
	}
	return title, string(full)
}

// PendingCounter counts requests seen pending and not yet decided, from
// live notifications only.
type PendingCounter struct{ ids map[string]bool }

func (c *PendingCounter) Add(id string) {
	if c.ids == nil {
		c.ids = map[string]bool{}
	}
	c.ids[id] = true
}

func (c *PendingCounter) Remove(id string) { delete(c.ids, id) }
func (c *PendingCounter) Count() int       { return len(c.ids) }
func (c *PendingCounter) Reset()           { c.ids = nil }
