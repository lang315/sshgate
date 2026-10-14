package model

import (
	"testing"

	"github.com/lang315/sshgate/app/hubapi"
)

func TestScreenFor(t *testing.T) {
	running := HubState{Kind: "running"}
	st := func(locked, vault bool) *hubapi.Status { return &hubapi.Status{Locked: locked, HasVault: vault} }
	for _, tc := range []struct {
		h    HubState
		s    *hubapi.Status
		want Screen
	}{
		{HubState{Kind: "starting"}, nil, ScreenHub},
		{HubState{Kind: "failed"}, st(false, true), ScreenHub},
		{running, nil, ScreenHub},
		{running, st(false, false), ScreenCreateVault},
		{running, st(true, true), ScreenLocked},
		{running, st(false, true), ScreenReady},
	} {
		if got := ScreenFor(tc.h, tc.s); got != tc.want {
			t.Errorf("%+v %+v: %s, want %s", tc.h, tc.s, got, tc.want)
		}
	}
}

func TestLockKind(t *testing.T) {
	if LockKind("idle", "") != "idle" || LockKind("manual", "") != "manual" || LockKind("", "") != "manual" || LockKind("weird", "idle") != "manual" {
		t.Fatal("basic")
	}
	for _, r := range []string{"grantsEnded", "softLockLimit"} {
		if LockKind(r, "manual") != "manual" || LockKind(r, "idle") != "idle" || LockKind(r, "") != "manual" {
			t.Fatal(r)
		}
	}
}

func TestLockTitleAndAIButton(t *testing.T) {
	if LockTitle(1) != "Auto-allow keeps running while locked" || LockTitle(0) != "" {
		t.Fatal("LockTitle")
	}
	if !AIButtonWaiting(false, 1) || AIButtonWaiting(true, 1) || AIButtonWaiting(false, 0) {
		t.Fatal("AIButtonWaiting")
	}
}

func TestHubStateFrom(t *testing.T) {
	h := HubStateFrom("failed", 0, 0, "the hub crashed 4 times in 60 s\nboom\nbang")
	if h.Message != "the hub crashed 4 times in 60 s" || h.Stderr != "boom\nbang" {
		t.Fatalf("%+v", h)
	}
	if h := HubStateFrom("failed", 0, 0, "hub speaks protocol 11"); h.Stderr != "" {
		t.Fatalf("%+v", h)
	}
}
