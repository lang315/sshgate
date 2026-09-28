package sshtest

import (
	"io"
	"net"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
)

// RefuseForward makes every later tcpip-forward request fail.
func (s *Server) RefuseForward() { s.mu.Lock(); s.refuseFwd = true; s.mu.Unlock() }

// StallDirect makes every later direct-tcpip channel open never answered:
// the handler blocks until the server stops, then returns.
func (s *Server) StallDirect() { s.mu.Lock(); s.stallDirect = true; s.mu.Unlock() }

// pipe copies both ways. A direction that ends at a clean EOF half-closes its
// destination, so the other direction can still carry a reply; an error, or
// a destination with no CloseWrite, closes both ends.
func pipe(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	half := func(dst, src io.ReadWriteCloser) {
		defer func() { done <- struct{}{} }()
		if _, err := io.Copy(dst, src); err == nil {
			if cw, ok := dst.(interface{ CloseWrite() error }); ok && cw.CloseWrite() == nil {
				return
			}
		}
		a.Close()
		b.Close()
	}
	go half(a, b)
	go half(b, a)
	<-done
	<-done
	a.Close()
	b.Close()
}

func (s *Server) directTCPIP(nch ssh.NewChannel) {
	var p struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nch.ExtraData(), &p); err != nil {
		nch.Reject(ssh.ConnectionFailed, "bad payload")
		return
	}
	s.mu.Lock()
	stall := s.stallDirect
	s.mu.Unlock()
	if stall {
		<-s.done
		return
	}
	out, err := net.Dial("tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))))
	if err != nil {
		nch.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nch.Accept()
	if err != nil {
		out.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	pipe(ch, out)
}

// globalRequests serves tcpip-forward for 127.0.0.1/localhost only, like an
// sshd with GatewayPorts no, and cancel-tcpip-forward.
func (s *Server) globalRequests(conn *ssh.ServerConn, reqs <-chan *ssh.Request) {
	var mu sync.Mutex
	lns := map[string]net.Listener{}
	defer func() {
		mu.Lock()
		for _, ln := range lns {
			ln.Close()
		}
		mu.Unlock()
	}()
	for req := range reqs {
		var p struct {
			Addr string
			Port uint32
		}
		switch req.Type {
		case "tcpip-forward":
			s.mu.Lock()
			refuse := s.refuseFwd
			s.mu.Unlock()
			if ssh.Unmarshal(req.Payload, &p) != nil || refuse || (p.Addr != "127.0.0.1" && p.Addr != "localhost") {
				req.Reply(false, nil)
				continue
			}
			ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(p.Port))))
			if err != nil {
				req.Reply(false, nil)
				continue
			}
			port := uint32(ln.Addr().(*net.TCPAddr).Port)
			mu.Lock()
			lns[net.JoinHostPort(p.Addr, strconv.Itoa(int(port)))] = ln
			mu.Unlock()
			if p.Port == 0 {
				req.Reply(true, ssh.Marshal(struct{ Port uint32 }{port}))
			} else {
				req.Reply(true, nil)
			}
			go func(addr string) {
				for {
					in, err := ln.Accept()
					if err != nil {
						return
					}
					ra := in.RemoteAddr().(*net.TCPAddr)
					ch, creqs, err := conn.OpenChannel("forwarded-tcpip", ssh.Marshal(struct {
						Addr     string
						Port     uint32
						OrigAddr string
						OrigPort uint32
					}{addr, port, ra.IP.String(), uint32(ra.Port)}))
					if err != nil {
						in.Close()
						continue
					}
					go ssh.DiscardRequests(creqs)
					go pipe(ch, in)
				}
			}(p.Addr)
		case "cancel-tcpip-forward":
			ssh.Unmarshal(req.Payload, &p)
			key := net.JoinHostPort(p.Addr, strconv.Itoa(int(p.Port)))
			mu.Lock()
			ln := lns[key]
			delete(lns, key)
			mu.Unlock()
			if ln != nil {
				ln.Close()
			}
			req.Reply(ln != nil, nil)
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}
