package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// InboundDevice is a device that may log in to this machine over SSH. The
// vault is the source of truth; the hub renders authorized_keys from it.
// An IP is in exactly one of Allowed and Blocked.
type InboundDevice struct {
	Name      string   `json:"name"`
	PublicKey string   `json:"publicKey"` // "<type> <base64>": no options, no comment
	Allowed   []string `json:"allowed,omitempty"`
	Blocked   []string `json:"blocked,omitempty"`
	Created   string   `json:"created"`
}

// MaxInboundIPs caps Allowed plus Blocked per device.
const MaxInboundIPs = 32

var (
	ErrInboundExists = errors.New("a device with this name already exists")
	ErrNoInbound     = errors.New("no such device")
	ErrInboundKeyDup = errors.New("this public key is already on another device")
	ErrNoInboundIP   = errors.New("this device has no such IP")
	ErrInboundIPCap  = fmt.Errorf("a device holds at most %d IP addresses", MaxInboundIPs)
)

// CanonInboundKey parses one authorized_keys public key and returns its
// stored form, "<type> <base64>". Options, a comment, a second line or
// trailing text are refused, so nothing but the key reaches authorized_keys.
func CanonInboundKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "\r\n\"") {
		return "", errors.New("public key: paste one line, \"<type> <base64>\"")
	}
	key, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(s))
	if err != nil {
		return "", errors.New("public key: not a valid SSH public key")
	}
	if len(options) > 0 || len(rest) > 0 {
		return "", errors.New("public key: options are not allowed")
	}
	_ = comment // dropped
	return string(bytes.TrimSpace(ssh.MarshalAuthorizedKey(key))), nil
}

// InboundFingerprint is the SHA256 fingerprint of a stored public key.
func InboundFingerprint(publicKey string) string {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKey))
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(key)
}

// CanonInboundIP returns the canonical form of an address or a CIDR prefix.
// "Any address" (a /0 prefix) and zoned addresses are refused.
func CanonInboundIP(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil || p.Addr().Zone() != "" {
			return "", errors.New("ip: not a valid address or prefix")
		}
		if p.Bits() == 0 {
			return "", errors.New("ip: a prefix that matches every address is not allowed; type real addresses")
		}
		return p.Masked().String(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return "", errors.New("ip: not a valid address or prefix")
	}
	return a.Unmap().String(), nil
}

func (f *File) findInbound(name string) *InboundDevice {
	for i := range f.Inbound {
		if f.Inbound[i].Name == name {
			return &f.Inbound[i]
		}
	}
	return nil
}

// AddInbound appends a device with one allowed IP. It does not save the file.
func AddInbound(f *File, name, publicKey, ip string, now time.Time) error {
	if err := ValidKeyName(name); err != nil {
		return errors.New(strings.Replace(err.Error(), "key name", "device name", 1))
	}
	key, err := CanonInboundKey(publicKey)
	if err != nil {
		return err
	}
	ip, err = CanonInboundIP(ip)
	if err != nil {
		return err
	}
	if f.findInbound(name) != nil {
		return ErrInboundExists
	}
	for _, d := range f.Inbound {
		if d.PublicKey == key {
			return ErrInboundKeyDup
		}
	}
	f.Inbound = append(f.Inbound, InboundDevice{Name: name, PublicKey: key, Allowed: []string{ip}, Created: now.UTC().Format(time.RFC3339)})
	return nil
}

// DeleteInbound removes a device.
func DeleteInbound(f *File, name string) error {
	for i := range f.Inbound {
		if f.Inbound[i].Name == name {
			f.Inbound = slices.Delete(f.Inbound, i, i+1)
			return nil
		}
	}
	return ErrNoInbound
}

// SetInboundIP puts ip in state "allowed" or "blocked" (adding or moving it)
// or forgets it ("removed"). It returns the canonical IP.
func SetInboundIP(f *File, name, ip, state string) (string, error) {
	d := f.findInbound(name)
	if d == nil {
		return "", ErrNoInbound
	}
	ip, err := CanonInboundIP(ip)
	if err != nil {
		return "", err
	}
	known := slices.Contains(d.Allowed, ip) || slices.Contains(d.Blocked, ip)
	switch state {
	case "allowed", "blocked", "removed":
	default:
		return "", errors.New("state must be allowed, blocked, or removed")
	}
	if state == "removed" && !known {
		return "", ErrNoInboundIP
	}
	if state != "removed" && !known && len(d.Allowed)+len(d.Blocked) >= MaxInboundIPs {
		return "", ErrInboundIPCap
	}
	d.Allowed = slices.DeleteFunc(d.Allowed, func(s string) bool { return s == ip })
	d.Blocked = slices.DeleteFunc(d.Blocked, func(s string) bool { return s == ip })
	switch state {
	case "allowed":
		d.Allowed = append(d.Allowed, ip)
	case "blocked":
		d.Blocked = append(d.Blocked, ip)
	}
	return ip, nil
}
