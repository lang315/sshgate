//go:build !unix

package hub

import "os"

func ownedByUs(os.FileInfo) bool { return true }
