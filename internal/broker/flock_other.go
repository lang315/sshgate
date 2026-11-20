//go:build !unix

package broker

import "os"

// lockFile is a no-op off unix: `sshgate inbound` does not run there.
func lockFile(*os.File) (func(), error) { return func() {}, nil }
