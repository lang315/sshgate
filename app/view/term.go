package view

import (
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/termconn"
)

// TerminalOptions wires a hub terminal into mygo's terminal: any real key,
// paste or input-method commit marks the connection's writes as the user's
// (the hub's idle lock counts only those). onExit, when set, runs on the
// terminal's goroutine once the connection ended.
func TerminalOptions(conn *termconn.Conn, onExit func()) terminal.Options {
	o := terminal.Options{
		Conn:    conn,
		OnKey:   func(_ ui.Modifiers, _ ui.Key) bool { conn.MarkUser(); return false },
		OnPaste: func(transfer.Data) bool { conn.MarkUser(); return false },
		OnInput: func(string) { conn.MarkUser() },
		// Links stay refused, as in the Electron app; the spec's http/https
		// confirm is not in a slice yet.
		OpenLink: func(string) bool { return true },
		// A remote program must not set the local clipboard (OSC 52).
		NoClipboardWrite: true,
	}
	if onExit != nil {
		o.OnExit = func(int) { onExit() }
	}
	return o
}
