package model

import (
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

// ringSpan is the grant length a full ring stands for: the longest timed mode.
const ringSpan = 4 * time.Hour

// Ring is the arc drawn around a host's tile while a grant is on or paused.
type Ring struct {
	Show, Dashed bool
	Frac         float64 // part of the circle, 0 to 1
}

// GrantRing says what the tile's ring shows for a grant at now.
func GrantRing(a *hubapi.AutoAllow, now time.Time) Ring {
	switch {
	case a == nil:
		return Ring{}
	case a.Paused:
		return Ring{Show: true, Dashed: true, Frac: 1}
	case a.Forever:
		return Ring{Show: true, Frac: 1}
	}
	l := left(a, now)
	if l <= 0 {
		return Ring{}
	}
	return Ring{Show: true, Frac: min(1, float64(l)/float64(ringSpan))}
}
