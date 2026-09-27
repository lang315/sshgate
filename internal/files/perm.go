package files

import "io/fs"

// uploadMode keeps rwx for user, group, and other; never setuid, setgid, sticky.
func uploadMode(m fs.FileMode) fs.FileMode { return m & fs.ModePerm }

// downloadMode: at most rwxr-xr-x; the server does not choose group/other write.
func downloadMode(m fs.FileMode) fs.FileMode { return m & 0o755 }
