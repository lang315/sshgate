package view

import (
	"reflect"
	"testing"

	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/hubproc"
)

func TestShellCountResetsOnRestart(t *testing.T) {
	d := &fakeDoor{status: hubapi.Status{HasVault: true}}
	a, tt, _, sh := newApp(t, d)
	note(t, a, tt, "pending", map[string]any{"request": reqN("r1", "box", "ls")})
	note(t, a, tt, "pending", map[string]any{"request": reqN("r2", "box", "id")})
	a.Update(func() { a.HubState(hubproc.State{Kind: "restarting", Attempt: 1}) })
	tt.flush()
	a.Update(func() { a.HubState(hubproc.State{Kind: "running"}) })
	tt.flush()
	note(t, a, tt, "pending", map[string]any{"request": reqN("r3", "box", "pwd")})
	if !reflect.DeepEqual(sh.pending, []int{1, 2, 0, 1}) {
		t.Fatalf("tray counts %v", sh.pending)
	}
}
