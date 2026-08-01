package config

// RecordHostKey pins a learned host-key fingerprint for server `name` in the
// store at `path`, but ONLY if that server currently has no pinned HostKey
// (first-use TOFU); later connects then enforce the pin and hard-fail on change.
// No-op if the server already has a HostKey or is not in the store. Serialized
// against other writers with a file lock.
func RecordHostKey(path, name, fingerprint string, masterKey []byte) error {
	return withFlock(path, func() error {
		f, err := Load(path)
		if err != nil {
			return err
		}
		for i := range f.Servers {
			if f.Servers[i].Name == name {
				if f.Servers[i].HostKey != "" {
					return nil
				}
				f.Servers[i].HostKey = fingerprint
				return Save(path, f, masterKey)
			}
		}
		return nil
	})
}
