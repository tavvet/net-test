package probe

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// ASN enrichment via Team Cymru's free DNS service. No API key, no rate-limit
// account, fits the project's "stdlib + DNS" feel. Two TXT queries:
//
//   <reversed-ipv4>.origin.asn.cymru.com  → "ASN | prefix | country | reg | date"
//   AS<n>.asn.cymru.com                   → "ASN | country | reg | date | name"
//
// The lookup runs in the background like the reverse-DNS cache: probes never
// block on it, and a lookup that hasn't returned yet just leaves the hop with
// an empty ASN/ASName (UI shows nothing extra until it lands).

// asnInfo is the result of resolving an IP to its origin AS.
type asnInfo struct {
	num  string // "AS13335"; empty means unknown or skipped
	name string // "CLOUDFLARENET - Cloudflare, Inc., US"
}

// asnCache resolves IPs to AS metadata lazily and remembers the result.
// Concurrent lookups for the same IP are deduplicated.
type asnCache struct {
	mu      sync.Mutex
	results map[string]asnInfo
	pending map[string]struct{}
}

func newASNCache() *asnCache {
	return &asnCache{
		results: map[string]asnInfo{},
		pending: map[string]struct{}{},
	}
}

// lookup returns the cached ASN info for ip. On the first call for a public
// IP it kicks off a background resolution and returns an empty value; private
// IPs (RFC1918, loopback, link-local) are cached as empty immediately — no
// DNS query is made for them.
func (c *asnCache) lookup(ip string) asnInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if info, ok := c.results[ip]; ok {
		return info
	}
	parsed := net.ParseIP(ip)
	if parsed == nil || isLocalIP(parsed) {
		c.results[ip] = asnInfo{} // cache miss; no point retrying private/junk IPs
		return asnInfo{}
	}
	if _, busy := c.pending[ip]; !busy {
		c.pending[ip] = struct{}{}
		go c.resolve(ip)
	}
	return asnInfo{}
}

// resolve performs the two TXT lookups and stores the result. Errors and empty
// answers are stored as empty info so we don't keep retrying a dead IP.
func (c *asnCache) resolve(ip string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	info := asnInfo{}
	if num := lookupOriginASN(ctx, ip); num != "" {
		info.num = "AS" + num
		info.name = lookupASName(ctx, num)
	}
	c.mu.Lock()
	c.results[ip] = info
	delete(c.pending, ip)
	c.mu.Unlock()
}

// lookupOriginASN returns the AS number (without "AS" prefix) for an IPv4
// address, or "" on miss. The response format from Team Cymru is:
//
//	"<asn> | <prefix> | <country> | <registry> | <date>"
//
// Some prefixes belong to multiple ASNs (e.g. anycast); we take the first.
func lookupOriginASN(ctx context.Context, ip string) string {
	host := reverseIPv4(ip) + ".origin.asn.cymru.com"
	txts, err := net.DefaultResolver.LookupTXT(ctx, host)
	if err != nil || len(txts) == 0 {
		return ""
	}
	fields := splitCymru(txts[0])
	if len(fields) < 1 {
		return ""
	}
	// Some records list multiple ASNs separated by spaces — take the first.
	asn := strings.Fields(fields[0])
	if len(asn) == 0 {
		return ""
	}
	return asn[0]
}

// lookupASName returns the human-readable AS name for an ASN, or "" on miss.
// Response format:
//
//	"<asn> | <country> | <registry> | <date> | <as-name>"
func lookupASName(ctx context.Context, asn string) string {
	host := "AS" + asn + ".asn.cymru.com"
	txts, err := net.DefaultResolver.LookupTXT(ctx, host)
	if err != nil || len(txts) == 0 {
		return ""
	}
	fields := splitCymru(txts[0])
	if len(fields) < 5 {
		return ""
	}
	return fields[4]
}

// splitCymru splits a Team Cymru TXT record on "|" and trims whitespace.
func splitCymru(s string) []string {
	parts := strings.Split(s, "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// reverseIPv4 returns "d.c.b.a" for "a.b.c.d", needed for the origin DNS query.
func reverseIPv4(ip string) string {
	octets := strings.Split(ip, ".")
	if len(octets) != 4 {
		return ip
	}
	return octets[3] + "." + octets[2] + "." + octets[1] + "." + octets[0]
}

// isLocalIP reports whether ip is in a non-routable range (RFC1918 private,
// loopback, link-local, CGNAT, or multicast/reserved). For these we skip the
// ASN query — they don't have a public AS, and the UI labels them separately.
func isLocalIP(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsMulticast() || ip.IsUnspecified() || isCGNAT(ip)
}

// isCGNAT covers 100.64.0.0/10 — Carrier-Grade NAT, common on mobile networks.
// net.IP doesn't classify it as private, but it has no public ASN either.
func isCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}
