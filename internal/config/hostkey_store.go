package config

import "errors"

// ErrPinSkipped means RecordHostKey changed nothing: the server is gone, is
// already pinned, or no longer points at the endpoint that was dialled.
var ErrPinSkipped = errors.New("host key not recorded: the server changed or is already pinned")

// RecordHostKey pins fingerprint (and its key type, "" if unknown) for
// server name, but only while it has no pin and still has host and port, so
// an edit that lands mid-dial never gets the old endpoint's key.
func RecordHostKey(path, name, host string, port int, fingerprint, algo string, masterKey []byte) error {
	return Update(path, masterKey, func(f *File) error {
		for i := range f.Servers {
			s := &f.Servers[i]
			if s.Name == name && s.HostKey == "" && s.Host == host && s.Port == port {
				s.HostKey, s.HostKeyAlgo = fingerprint, algo
				return nil
			}
		}
		return ErrPinSkipped
	})
}
