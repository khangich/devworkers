package util

import (
	"strings"
	"time"
)

// Timestamp returns the run directory suffix.
func Timestamp() string {
	return time.Now().UTC().Format("2006-01-02T15-04-05Z")
}

// ResolveLocation maps a human readable timezone to a Go location.
func ResolveLocation(name string) *time.Location {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "", "local":
		return time.Local
	case "utc":
		return time.UTC
	default:
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
		return time.Local
	}
}
