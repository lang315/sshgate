package config

import "errors"

// ErrPinSkipped means RecordHostKey changed nothing: the server is gone, is
// already pinned, or no longer points at the endpoint that was dialled.
var ErrPinSkipped = errors.New("host key not recorded: the server changed or is already pinned")

// errSamePin aborts the Update without a write: the pin is already there.
var errSamePin = errors.New("already pinned to this key")

// RecordHostKey pins fingerprint (and its key type, "" if unknown) for
// server name, but only while it has no pin and still has host and port, so
// an edit that lands mid-dial never gets the old endpoint's key. A server
// already pinned to exactly this key at that endpoint is success, with no
// write: two trusted opens of one key may race.
func RecordHostKey(path, name, host string, port int, fingerprint, algo string, masterKey []byte) error {
	err := Update(path, masterKey, func(f *File) error {
		for i := range f.Servers {
			s := &f.Servers[i]
			if s.Name == name && s.HostKey == "" && s.Host == host && s.Port == port {
				s.HostKey, s.HostKeyAlgo = fingerprint, algo
				return nil
			}
			if s.Name == name && s.HostKey == fingerprint && s.HostKeyAlgo == algo && s.Host == host && s.Port == port {
				return errSamePin
			}
		}
		return ErrPinSkipped
	})
	if errors.Is(err, errSamePin) {
		return nil
	}
	return err
}
