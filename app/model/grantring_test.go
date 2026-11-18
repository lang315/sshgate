package model

import (
	"testing"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

func TestGrantRing(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	in := func(d time.Duration) *hubapi.AutoAllow {
		return &hubapi.AutoAllow{Until: now.Add(d).Format(time.RFC3339)}
	}
	for _, c := range []struct {
		name string
		a    *hubapi.AutoAllow
		want Ring
	}{
		{"nil", nil, Ring{}},
		{"forever", &hubapi.AutoAllow{Forever: true}, Ring{true, false, 1}},
		{"paused", &hubapi.AutoAllow{Forever: true, Paused: true}, Ring{true, true, 1}},
		{"1h", in(time.Hour), Ring{true, false, 0.25}},
		{"15m", in(15 * time.Minute), Ring{true, false, 0.0625}},
		{"6h", in(6 * time.Hour), Ring{true, false, 1}},
		{"past", in(-time.Second), Ring{}},
		{"junk", &hubapi.AutoAllow{Until: "junk"}, Ring{}},
	} {
		if got := GrantRing(c.a, now); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}
