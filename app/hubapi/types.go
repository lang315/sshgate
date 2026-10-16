// Package hubapi is the typed side of sshgate hub's UI door (protocol 12)
// that the desktop app's screens use.
package hubapi

import "time"

const (
	Allowed   = "allowed"
	Denied    = "denied"
	SentToTab = "sent_to_tab"
)

type Status struct {
	Locked        bool     `json:"locked"`
	HasStore      bool     `json:"hasStore"`
	HasVault      bool     `json:"hasVault"`
	StorePath     string   `json:"storePath"`
	Pending       int      `json:"pending"`
	StoreError    string   `json:"storeError,omitempty"`
	AutoHosts     []string `json:"autoHosts,omitempty"`     // only while soft-locked
	SoftLockLimit string   `json:"softLockLimit,omitempty"` // only while unlocked
}

type AutoAllow struct {
	Until   string `json:"until,omitempty"` // RFC3339, a live timed grant
	Forever bool   `json:"forever,omitempty"`
	Paused  bool   `json:"paused,omitempty"`
}

type Server struct {
	Name             string     `json:"name"`
	Host             string     `json:"host"`
	Port             int        `json:"port"`
	User             string     `json:"user"`
	Auth             string     `json:"auth"`
	KeyPath          string     `json:"keyPath"`
	HostKey          string     `json:"hostKey"`
	HostKeyAlgo      string     `json:"hostKeyAlgo"`
	AIVisible        bool       `json:"aiVisible"`
	Locked           bool       `json:"locked"`
	AutoAllow        *AutoAllow `json:"autoAllow,omitempty"`
	AutoAllowRefused string     `json:"autoAllowRefused,omitempty"`
}

type Request struct {
	ID          string    `json:"id"`
	Client      string    `json:"client"`
	Server      string    `json:"server"`
	Target      string    `json:"target"`
	Command     string    `json:"command"`
	Description string    `json:"description"`
	Stdin       string    `json:"stdin,omitempty"`
	Sudo        bool      `json:"sudo"`
	TimeoutSec  int       `json:"timeoutSec"`
	ReceivedAt  time.Time `json:"receivedAt"`
}

type Decision struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

type AutoRan struct {
	Server      string `json:"server"`
	Command     string `json:"command"`
	Truncated   int    `json:"truncated,omitempty"`
	Description string `json:"description"`
	ExitCode    *int   `json:"exitCode,omitempty"` // 0 is a real exit code; absent when Error is set
	Error       string `json:"error,omitempty"`
	Time        string `json:"time"`
	Sudo        bool   `json:"sudo,omitempty"`
	StdinBytes  int    `json:"stdinBytes,omitempty"`
}

type RanLocked struct {
	Server string `json:"server"`
	Count  int    `json:"count"`
}

type UnlockReply struct {
	RanWhileLocked []RanLocked `json:"ranWhileLocked,omitempty"`
}

// HostKey is a term.open result other than "open". hostKeyUnknown carries
// Fingerprint, KeyType and KnownHosts ("match", "different" or "absent", a
// hint from ~/.ssh/known_hosts, never pinned from); hostKeyMismatch carries
// Pinned and Presented.
type HostKey struct {
	Status      string `json:"status"`
	Server      string `json:"server"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Fingerprint string `json:"fingerprint,omitempty"`
	KeyType     string `json:"keyType,omitempty"`
	KnownHosts  string `json:"knownHosts,omitempty"`
	Pinned      string `json:"pinned,omitempty"`
	Presented   string `json:"presented,omitempty"`
}

// Trust is term.open's trustHostKey: the key the person checked, which the
// hub pins once a dial pinned to exactly it succeeds.
type Trust struct {
	Fingerprint string `json:"fingerprint"`
	KeyType     string `json:"keyType"`
}
