// Package hubapi is the typed side of sshgate hub's UI door (protocol 12)
// that the desktop app's screens use.
package hubapi

import (
	"encoding/json"
	"time"
)

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
	HasPassword      bool       `json:"hasPassword"`
	HasSuPassword    bool       `json:"hasSuPassword"`
	HasSudoPassword  bool       `json:"hasSudoPassword"`
	HasKeyPassphrase bool       `json:"hasKeyPassphrase"`
	AutoAllowRoot    bool       `json:"autoAllowRoot"`
	AutoAllowSudo    bool       `json:"autoAllowSudo"`
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

// ServerInput is servers.save's server. The four secrets are write-only:
// nil is omitted and keeps the stored value, "" clears it, anything else
// replaces it. The hub never sends one back.
type ServerInput struct {
	Name          string  `json:"name"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	User          string  `json:"user"`
	Auth          string  `json:"auth"`
	KeyPath       string  `json:"keyPath"`
	AIVisible     bool    `json:"aiVisible"`
	AutoAllowRoot bool    `json:"autoAllowRoot"`
	AutoAllowSudo bool    `json:"autoAllowSudo"`
	Password      *string `json:"password,omitempty"`
	SuPassword    *string `json:"suPassword,omitempty"`
	SudoPassword  *string `json:"sudoPassword,omitempty"`
	KeyPassphrase *string `json:"keyPassphrase,omitempty"`
}

// Candidate is one alias of import.scan. Status is "ready", "exists" (a
// server has this name) or "skipped" (Reason says why).
type Candidate struct {
	Alias           string `json:"alias"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	User            string `json:"user"`
	Auth            string `json:"auth"`
	KeyPath         string `json:"keyPath,omitempty"`
	NeedsPassphrase bool   `json:"needsPassphrase,omitempty"`
	HostKey         string `json:"hostKey,omitempty"`
	HostKeyAlgo     string `json:"hostKeyAlgo,omitempty"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
}

type ImportScan struct {
	Candidates []Candidate `json:"candidates"`
	Note       string      `json:"note,omitempty"` // "No ~/.ssh/config"
}

type ImportSkip struct {
	Alias  string `json:"alias"`
	Reason string `json:"reason"`
}

type ImportResult struct {
	Imported []string     `json:"imported"`
	Skipped  []ImportSkip `json:"skipped"`
}

// AutoCheck is servers.autoAllowCheck's answer: the uid the server's login
// runs as, and whether sudo asks it no password. Advice for the Auto-allow
// dialog's root warning, never a gate.
type AutoCheck struct {
	UID              int  `json:"uid"`
	PasswordlessSudo bool `json:"passwordlessSudo"`
}

// FileEntry is one name of a files.list answer. Mode holds the permission
// bits only; Mtime is in Unix seconds; Kind is "dir", "file", "link" or
// "other"; Target is a link's target, else "".
type FileEntry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
	Mtime  int64  `json:"mtime"`
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
}

// FileListing is files.list's answer. Status is "listed", or a host-key
// result as term.open's ("hostKeyUnknown", "hostKeyMismatch"), whose
// details fill the embedded HostKey. Path is the folder actually listed;
// Bad counts the names left out as unsafe to show or copy.
type FileListing struct {
	HostKey
	Path      string      `json:"path"`
	Entries   []FileEntry `json:"entries"`
	Truncated bool        `json:"truncated"`
	Bad       int         `json:"bad"`
}

// FilesPlanned is files.planned: what a job's walk found. Conflicts are
// existing files at the destination; Sample holds up to 5, relative to it.
type FilesPlanned struct {
	ID        string `json:"id"`
	Files     int    `json:"files"`
	Dirs      int    `json:"dirs"`
	Links     int    `json:"links"`
	Bytes     int64  `json:"bytes"`
	Conflicts struct {
		Count  int      `json:"count"`
		Sample []string `json:"sample"`
	} `json:"conflicts"`
	ErrorCount int      `json:"errorCount"`
	Errors     []string `json:"errors"`
}

// FilesProgress is files.progress: Done bytes of the job's Total, and the
// file being copied, relative to the destination. Never sent for a delete.
type FilesProgress struct {
	ID    string `json:"id"`
	File  string `json:"file"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
}

// FilesDone is files.done, sent once per job the hub took. Reason is ""
// for a job that ran to its end; "cancelled", "server changed", "plan
// expired", "app closed", or the error that ended it otherwise.
type FilesDone struct {
	ID         string   `json:"id"`
	Op         string   `json:"op"`
	Copied     int      `json:"copied"`
	Skipped    int      `json:"skipped"`
	Deleted    int      `json:"deleted"`
	Bytes      int64    `json:"bytes"`
	ErrorCount int      `json:"errorCount"`
	Errors     []string `json:"errors"`
	Cancelled  bool     `json:"cancelled"`
	Reason     string   `json:"reason"`
}

// Tunnel is one of a server's saved forwards (config.Tunnel). Kind is
// "local", "remote" or "dynamic" (SOCKS5, no target); the listen side is
// always 127.0.0.1. ID is "" for a new tunnel: the hub gives it one.
type Tunnel struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	ListenPort int    `json:"listenPort"`
	TargetHost string `json:"targetHost"`
	TargetPort int    `json:"targetPort"`
	Label      string `json:"label"`
}

// TunnelState is tunnels.state: Status is "stopped", "starting",
// "running" or "error" (Error says why); Conns counts the open connections
// of a running tunnel.
type TunnelState struct {
	Server string `json:"server"`
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Conns  int    `json:"conns"`
}

// TunnelView is one row of tunnels.list: a saved tunnel and its state.
type TunnelView struct {
	Server string `json:"server"`
	Tunnel
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Conns  int    `json:"conns"`
}

// AuditQuery is audit.read's params: records older than Before (0 is from
// the newest), at most Limit (0 is the hub's 200), narrowed by Server, and
// a union of Kinds ("exec", "config", "file", "tunnel") and exec Outcomes
// ("allowed", "auto", "denied", "expired", "cancelled", "error"), then by
// Text. The hub decodes its params strictly: only these keys, each left
// out when empty.
type AuditQuery struct {
	Before   int      `json:"before,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	Server   string   `json:"server,omitempty"`
	Kinds    []string `json:"kinds,omitempty"`
	Outcomes []string `json:"outcomes,omitempty"`
	Text     string   `json:"text,omitempty"`
}

// AuditEntry is one audit.jsonl record and its 1-based line number; it is
// also audit.appended's params. Record is the line as stored: the file can
// be edited by hand, so model reads it without trusting a field's type.
type AuditEntry struct {
	Seq    int             `json:"seq"`
	Record json.RawMessage `json:"record"`
}

// AuditPage is audit.read's reply: Records newest first; Next, when not 0,
// is the Before that reads the older ones; Skipped counts the lines this
// read scanned that did not parse; Path is the audit file's.
type AuditPage struct {
	Records []AuditEntry `json:"records"`
	Next    int          `json:"next,omitempty"`
	Skipped int          `json:"skipped"`
	Path    string       `json:"path"`
}
