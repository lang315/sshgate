package config

import "errors"

// MaxTunnels caps the tunnels one server holds.
const MaxTunnels = 32

// Tunnel is a saved port forward. Not a secret: it lives in plain text in
// the MAC-covered store. The bind address is not a field: the hub binds
// only 127.0.0.1 (see slice 3b spec).
type Tunnel struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"` // local, remote, dynamic
	ListenPort int    `json:"listenPort"`
	TargetHost string `json:"targetHost,omitempty"`
	TargetPort int    `json:"targetPort,omitempty"`
	Label      string `json:"label,omitempty"`
}

// Validate names the first bad field. The ID is the hub's to check.
func (t Tunnel) Validate() error {
	_, _, badLabel := forbiddenRune(t.Label)
	switch {
	case t.Kind != "local" && t.Kind != "remote" && t.Kind != "dynamic":
		return errors.New("kind: must be local, remote, or dynamic")
	case t.ListenPort < 1 || t.ListenPort > 65535:
		return errors.New("listenPort: must be 1-65535")
	case t.Kind == "dynamic" && (t.TargetHost != "" || t.TargetPort != 0):
		return errors.New("dynamic: takes no target")
	case t.Kind != "dynamic" && (!plainToken(t.TargetHost) || len(t.TargetHost) > 253):
		return errors.New("targetHost: required, at most 253 bytes, with no spaces or control characters")
	case t.Kind != "dynamic" && (t.TargetPort < 1 || t.TargetPort > 65535):
		return errors.New("targetPort: must be 1-65535")
	case len(t.Label) > 64 || badLabel:
		return errors.New("label: at most 64 bytes, with no control characters")
	}
	return nil
}
