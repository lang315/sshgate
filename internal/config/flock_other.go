//go:build !unix

package config

// ponytail: duplicated from internal/web to avoid a web→config layering issue; consolidate if a third user appears

// withFlock is a no-op on non-unix platforms; callers within a single
// process are not otherwise serialized here.
func withFlock(path string, fn func() error) error {
	return fn()
}
