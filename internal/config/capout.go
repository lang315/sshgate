package config

import "fmt"

const DefaultOutputCap = 64 * 1024

// CapOutput keeps the first and last max/2 bytes of s and replaces the
// middle with a marker naming how many bytes were dropped. Applied to
// stdout and stderr independently so one stream cannot evict the other.
func CapOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	half := max / 2
	dropped := len(s) - max
	return s[:half] + truncMarker(dropped) + s[len(s)-half:]
}

// truncMarker is the text CapOutput and RedactCap put where bytes were dropped.
func truncMarker(dropped int) string {
	return fmt.Sprintf("\n… [truncated %d bytes] …\n", dropped)
}
