package probe

import (
	"net"
	"strings"
	"time"
)

// IsLocalIP reports whether ip is in a non-routable range (RFC1918 private,
// loopback, link-local, CGNAT 100.64.0.0/10, multicast, unspecified). UI and
// report packages share this predicate so the classification stays consistent
// between TUI labels, JSON segments, and per-hop diagnostics.
func IsLocalIP(ip net.IP) bool { return isLocalIP(ip) }

// ShortenASName trims the verbose form Team Cymru returns to its leading
// token. Examples:
//
//	"CLOUDFLARENET - Cloudflare, Inc., US" → "CLOUDFLARENET"
//	"GOOGLE, US"                           → "GOOGLE"
//
// Shared so the TUI's per-hop suffix and the diagnosis labels stay in sync.
func ShortenASName(name string) string {
	if i := strings.Index(name, " - "); i > 0 {
		return name[:i]
	}
	if i := strings.Index(name, ","); i > 0 {
		return name[:i]
	}
	return name
}

// Millis converts a Duration to milliseconds as a plain float64. Zero or
// negative durations return 0. UI uses this raw value for inline rendering;
// the report layer rounds to one decimal on top.
func Millis(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(d) / float64(time.Millisecond)
}
