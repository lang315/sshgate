package view

import (
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/termconn"
)

// TerminalOptions wires a hub terminal into mygo's terminal: any real key,
// paste or input-method commit marks the connection's writes as the user's
// (the hub's idle lock counts only those).
func TerminalOptions(conn *termconn.Conn) terminal.Options {
	return terminal.Options{
		Conn:     conn,
		OnKey:    func(_ ui.Modifiers, _ ui.Key) bool { conn.MarkUser(); return false },
		OnPaste:  func(string) bool { conn.MarkUser(); return false },
		OnInput:  func(string) { conn.MarkUser() },
		OpenLink: func(string) bool { return true }, // slice 0 opens no links; the confirm comes in slice 2
	}
}
