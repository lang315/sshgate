//go:build !unix

package config

// Only Update calls withFlock; every store writer goes through Update.

// withFlock is a no-op on non-unix platforms; callers within a single
// process are not otherwise serialized here.
func withFlock(path string, fn func() error) error {
	return fn()
}
