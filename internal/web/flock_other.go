//go:build !unix

package web

// withFlock is a no-op on non-unix platforms; the in-process mutex in App
// still serializes writers within a single process.
func withFlock(path string, fn func() error) error {
	return fn()
}
