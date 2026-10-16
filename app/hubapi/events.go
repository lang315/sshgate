package hubapi

import "encoding/json"

type PendingEvent struct {
	Request Request `json:"request"`
}

type DecidedEvent struct {
	Request  Request  `json:"request"`
	Decision Decision `json:"decision"`
}

type LockedEvent struct {
	Reason string `json:"reason"`
}

type AutoOffEvent struct {
	Server string `json:"server"`
	Reason string `json:"reason"`
}

type AutoRanEvent struct{ AutoRan }

// Decode turns a hub notification the approval core uses into its typed
// event. Anything else (term.*, files.*, tunnels.*, audit.appended) and
// malformed params report false.
func Decode(method string, params json.RawMessage) (any, bool) {
	var ev any
	switch method {
	case "pending":
		ev = &PendingEvent{}
	case "decided":
		ev = &DecidedEvent{}
	case "locked":
		ev = &LockedEvent{}
	case "autoAllow.off":
		ev = &AutoOffEvent{}
	case "autoAllow.ran":
		ev = &AutoRanEvent{}
	default:
		return nil, false
	}
	if err := json.Unmarshal(params, ev); err != nil {
		return nil, false
	}
	switch e := ev.(type) {
	case *PendingEvent:
		return *e, true
	case *DecidedEvent:
		return *e, true
	case *LockedEvent:
		return *e, true
	case *AutoOffEvent:
		return *e, true
	default:
		return *ev.(*AutoRanEvent), true
	}
}
