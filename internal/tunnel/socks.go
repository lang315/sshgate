package tunnel

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"time"
)

// SOCKS5 reply codes used here (RFC 1928 §6).
const (
	repOK          = 0x00
	repFailure     = 0x01
	repCmdNotSupp  = 0x07
	repAddrNotSupp = 0x08
)

var errSocks = errors.New("socks: bad request")

// socks runs the RFC 1928 handshake on in: version 5, method "no auth",
// CONNECT with an IPv4, IPv6 or domain address. A domain is passed to dial
// unresolved, so DNS happens on the server, as with ssh -D. It returns the
// dialled connection after writing the success reply.
func socks(in net.Conn, dial func(addr string) (net.Conn, error)) (net.Conn, error) {
	in.SetDeadline(time.Now().Add(10 * time.Second))
	defer in.SetDeadline(time.Time{})
	h := make([]byte, 2)
	if _, err := io.ReadFull(in, h); err != nil || h[0] != 5 {
		return nil, errSocks
	}
	methods := make([]byte, h[1])
	if _, err := io.ReadFull(in, methods); err != nil {
		return nil, errSocks
	}
	noAuth := false
	for _, m := range methods {
		noAuth = noAuth || m == 0
	}
	if !noAuth {
		in.Write([]byte{5, 0xff})
		return nil, errSocks
	}
	if _, err := in.Write([]byte{5, 0}); err != nil {
		return nil, err
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(in, req); err != nil || req[0] != 5 {
		return nil, errSocks
	}
	var host string
	switch req[3] {
	case 1, 4:
		ip := make([]byte, map[byte]int{1: 4, 4: 16}[req[3]])
		if _, err := io.ReadFull(in, ip); err != nil {
			return nil, errSocks
		}
		host = net.IP(ip).String()
	case 3:
		n := make([]byte, 1)
		if _, err := io.ReadFull(in, n); err != nil {
			return nil, errSocks
		}
		name := make([]byte, n[0])
		if _, err := io.ReadFull(in, name); err != nil {
			return nil, errSocks
		}
		host = string(name)
	default:
		reply(in, repAddrNotSupp)
		return nil, errSocks
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(in, pb); err != nil {
		return nil, errSocks
	}
	if req[1] != 1 {
		reply(in, repCmdNotSupp)
		return nil, errSocks
	}
	out, err := dial(net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb)))))
	if err != nil {
		reply(in, repFailure)
		return nil, err
	}
	if err := reply(in, repOK); err != nil {
		out.Close()
		return nil, err
	}
	return out, nil
}

// reply sends a reply with a zero IPv4 bind address; clients ignore it for CONNECT.
func reply(w io.Writer, code byte) error {
	_, err := w.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}
