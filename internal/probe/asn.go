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

// asnFailTTL is how long a failed lookup (timeout, NXDOMAIN, empty answer) is
// remembered before we try again. Successful and private results are cached
// for the whole session — ASN mappings change rarely. A short retry window
// keeps a single transient DNS hiccup from blanking a hop's AS for the rest
// of the run, without hammering Cymru on every cycle.
const asnFailTTL = 60 * time.Second

// asnEntry is a cached lookup. A zero expires means "permanent" (a success or
// a private IP); a non-zero expires marks a failure that should be retried
// once now() passes it.
type asnEntry struct {
	info    asnInfo
	expires time.Time
}

// asnCache resolves IPs to AS metadata lazily and remembers the result.
// Concurrent lookups for the same IP are deduplicated. The parent ctx is the
// Tracer's run context — cancellation aborts in-flight Cymru lookups instead
// of running to their own timeout.
type asnCache struct {
	mu      sync.Mutex
	results map[string]asnEntry
	pending map[string]struct{}
	parent  context.Context

	// Injectable for tests; default to the real clock / Cymru resolver.
	now       func() time.Time
	failTTL   time.Duration
	resolveFn func(ctx context.Context, ip string) (asnInfo, bool)
}

func newASNCache(parent context.Context) *asnCache {
	return &asnCache{
		results:   map[string]asnEntry{},
		pending:   map[string]struct{}{},
		parent:    parent,
		now:       time.Now,
		failTTL:   asnFailTTL,
		resolveFn: cymruResolve,
	}
}

// lookup returns the cached ASN info for ip. On the first call for a public
// IP it kicks off a background resolution and returns an empty value; private
// IPs (RFC1918, loopback, link-local) are cached permanently as empty — no
// DNS query is made for them. A previously-failed lookup whose TTL has expired
// is retried.
func (c *asnCache) lookup(ip string) asnInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.results[ip]; ok {
		if e.expires.IsZero() || c.now().Before(e.expires) {
			return e.info // permanent, or failure still within its TTL
		}
		delete(c.results, ip) // failure TTL expired → fall through and retry
	}
	parsed := net.ParseIP(ip)
	if parsed == nil || isLocalIP(parsed) {
		c.results[ip] = asnEntry{} // permanent: private/junk IPs have no public AS
		return asnInfo{}
	}
	if _, busy := c.pending[ip]; !busy {
		c.pending[ip] = struct{}{}
		go c.resolve(ip)
	}
	return asnInfo{}
}

// resolve performs the lookup in the background and caches the outcome:
// success permanently, failure with a retry TTL.
func (c *asnCache) resolve(ip string) {
	ctx, cancel := context.WithTimeout(c.parent, 3*time.Second)
	defer cancel()
	info, ok := c.resolveFn(ctx, ip)

	c.mu.Lock()
	if ok {
		c.results[ip] = asnEntry{info: info}
	} else {
		c.results[ip] = asnEntry{expires: c.now().Add(c.failTTL)}
	}
	delete(c.pending, ip)
	c.mu.Unlock()
}

// cymruResolve is the production resolver: two TXT queries to Team Cymru.
// Returns ok=false on any failure or empty answer.
func cymruResolve(ctx context.Context, ip string) (asnInfo, bool) {
	num := lookupOriginASN(ctx, ip)
	if num == "" {
		return asnInfo{}, false
	}
	return asnInfo{num: "AS" + num, name: lookupASName(ctx, num)}, true
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
