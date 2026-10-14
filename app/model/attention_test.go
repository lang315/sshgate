package model

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lang315/sshgate/app/hubapi"
)

func TestTrayTexts(t *testing.T) {
	if TrayTitle(0) != "" || TrayTitle(3) != "3" {
		t.Fatal("title")
	}
	if TrayTooltip(0) != "sshgate" || TrayTooltip(1) != "sshgate: 1 request waiting" || TrayTooltip(2) != "sshgate: 2 requests waiting" {
		t.Fatal("tooltip")
	}
}

func TestNotificationText(t *testing.T) {
	title, body := NotificationText(hubapi.Request{Server: "box", Command: strings.Repeat("x", 300), Sudo: true, Description: "SECRET-DESC", Stdin: "SECRET-STDIN"})
	if title != "AI wants to run a command with sudo" {
		t.Fatal(title)
	}
	if !strings.HasPrefix(body, "box: xxx") || utf8.RuneCountInString(body) > 121 || !strings.HasSuffix(body, "…") {
		t.Fatal(body)
	}
	if strings.Contains(body, "SECRET") {
		t.Fatal("description or stdin in the notification")
	}
	if title, _ := NotificationText(hubapi.Request{Server: "box", Command: "ls"}); title != "AI wants to run a command" {
		t.Fatal(title)
	}
}

func TestPendingCounter(t *testing.T) {
	var c PendingCounter
	c.Add("a")
	c.Add("a")
	c.Add("b")
	if c.Count() != 2 {
		t.Fatal(c.Count())
	}
	c.Remove("a")
	if c.Count() != 1 {
		t.Fatal(c.Count())
	}
	c.Reset()
	if c.Count() != 0 {
		t.Fatal(c.Count())
	}
}
