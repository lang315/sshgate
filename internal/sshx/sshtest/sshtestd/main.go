// Command sshtestd is a development and test SSH server. It accepts any
// password, echoes shell input (a "flood <N>" line writes N bytes, then
// FLOOD-DONE), and answers exec with the command text.
// With -write-store it writes a vault containing one AI-visible server
// "box" that points at itself, ready to unlock with -password.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/lang315/sshgate/internal/config"
	"github.com/lang315/sshgate/internal/sshx/sshtest"
)

func main() {
	store := flag.String("write-store", "", "write a ready vault to this path")
	pw := flag.String("password", "pw", "master password for -write-store")
	flag.Parse()

	srv, stop, err := sshtest.Listen()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer stop()
	close(srv.Release)
	go func() {
		for range srv.Execs {
		}
	}()
	if *store != "" {
		if err := writeStore(*store, *pw, srv); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("PORT=%d\n", srv.Port)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	eof := make(chan struct{})
	go func() { io.Copy(io.Discard, os.Stdin); close(eof) }()
	select {
	case <-sig:
	case <-eof:
	}
}

func writeStore(path, pw string, srv *sshtest.Server) error {
	k, mk, err := config.NewKDF(pw)
	if err != nil {
		return err
	}
	f := &config.File{Version: 1, KDF: &k, Servers: []config.Server{{
		Name: "box", Host: srv.Host, Port: srv.Port, User: "test", Auth: "password",
		AIVisible: true, HostKey: srv.Fingerprint(),
	}}}
	enc, err := config.Encrypt(mk, "box/encPassword", config.AADFor(f, f.Servers[0], "encPassword"), "testpass")
	if err != nil {
		return err
	}
	f.Servers[0].EncPassword = enc
	return config.Save(path, f, mk)
}
