//go:build race

package config

// raceOn: the race detector slows the pure single-goroutine sweeps about 24x
// and finds nothing in them, so they sample (CI also runs them without -race).
const raceOn = true
