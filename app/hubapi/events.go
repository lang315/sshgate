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

// Decode turns a hub notification the app uses into its typed event:
// the approval core's and files.planned, files.progress and files.done.
// Anything else (term.*, tunnels.*, audit.appended) and malformed params
// report false.
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
	case "files.planned":
		ev = &FilesPlanned{}
	case "files.progress":
		ev = &FilesProgress{}
	case "files.done":
		ev = &FilesDone{}
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
	case *FilesPlanned:
		return *e, true
	case *FilesProgress:
		return *e, true
	case *FilesDone:
		return *e, true
	default:
		return *ev.(*AutoRanEvent), true
	}
}
