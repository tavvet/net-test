package probe

import (
	"fmt"
	"net"
	"strings"
)

// SegmentKind classifies a contiguous group of hops by its role in the path.
// The classification is positional: the first non-local segment is the
// provider, the last segment is the destination, everything in between is
// transit. This is a simplification (an ISP segment in the middle is still
// "transit" here) but it matches what users intuitively want to see — "where
// in the path the problem is".
type SegmentKind int

const (
	// SegmentLocal — hops with private IPs (RFC1918, loopback, link-local, CGNAT).
	SegmentLocal SegmentKind = iota
	// SegmentProvider — the first non-local AS on the path — typically the ISP.
	SegmentProvider
	// SegmentTransit — intermediate AS segments between provider and destination.
	SegmentTransit
	// SegmentDestination — the AS containing the target.
	SegmentDestination
	// SegmentUnknown — public IPs whose ASN lookup hasn't returned yet (or failed).
	SegmentUnknown
)

func (k SegmentKind) String() string {
	switch k {
	case SegmentLocal:
		return "local"
	case SegmentProvider:
		return "provider"
	case SegmentTransit:
		return "transit"
	case SegmentDestination:
		return "destination"
	default:
		return "unknown"
	}
}

// Segment is one contiguous group of hops that all belong to the same network
// (private LAN or a single ASN). Healthy/Issue summarize the persistent
// anomaly state inside the segment.
type Segment struct {
	Label    string // human-readable, e.g. "Провайдер ITNET-AS" / "Cloudflare"
	Kind     SegmentKind
	HopFrom  int // TTL of the first hop in the segment (1-based)
	HopTo    int // TTL of the last hop in the segment
	HopCount int
	Healthy  bool   // true if no persistent anomaly was flagged inside the segment
	Issue    string // empty when Healthy; otherwise describes the first problem
}

// Diagnosis is the per-segment summary that the TUI's "Диагноз" tab and the
// JSON export render.
type Diagnosis struct {
	Segments []Segment
}

// BuildDiagnosis groups hops into segments by ASN (or "local" for private IPs)
// and produces a per-segment health verdict from the persistent-anomaly flags
// that markAnomalies has already attached to each hop. Silent hops (no IP yet)
// extend the current segment so a single unresponsive router doesn't fragment
// the route into spurious pieces.
func BuildDiagnosis(hops []Hop) Diagnosis {
	if len(hops) == 0 {
		return Diagnosis{}
	}

	type group struct {
		zone string
		hops []int // indices into the input slice
	}
	var groups []group

	for i, h := range hops {
		zone := hopZone(h)
		last := len(groups) - 1
		switch {
		case last < 0:
			groups = append(groups, group{zone: zone, hops: []int{i}})
		case zone == "" || zone == groups[last].zone:
			groups[last].hops = append(groups[last].hops, i)
		default:
			groups = append(groups, group{zone: zone, hops: []int{i}})
		}
	}

	// Build segments with kinds. The kind depends on position: first non-local
	// = provider; last = destination; everything between = transit.
	segments := make([]Segment, 0, len(groups))
	firstNonLocal := -1
	for i, g := range groups {
		if g.zone != "local" {
			firstNonLocal = i
			break
		}
	}

	for i, g := range groups {
		s := Segment{
			HopFrom:  hops[g.hops[0]].TTL,
			HopTo:    hops[g.hops[len(g.hops)-1]].TTL,
			HopCount: len(g.hops),
		}
		switch {
		case g.zone == "local":
			s.Kind = SegmentLocal
		case g.zone == "?":
			s.Kind = SegmentUnknown
		case i == len(groups)-1:
			s.Kind = SegmentDestination
		case i == firstNonLocal:
			s.Kind = SegmentProvider
		default:
			s.Kind = SegmentTransit
		}
		s.Label = segmentLabel(s.Kind, g.hops, hops)
		s.Healthy, s.Issue = segmentHealth(g.hops, hops)
		segments = append(segments, s)
	}
	return Diagnosis{Segments: segments}
}

// hopZone returns the grouping key for a hop: "local" for private IPs, the
// ASN string for public IPs once we know it, "?" for public IPs whose ASN
// hasn't resolved yet, and "" for silent hops (which should extend the current
// segment rather than start a new one).
func hopZone(h Hop) string {
	if h.IP == "" {
		return ""
	}
	if ip := net.ParseIP(h.IP); ip != nil && isLocalIP(ip) {
		return "local"
	}
	if h.ASN != "" {
		return h.ASN
	}
	return "?"
}

// segmentLabel composes a user-facing name for the segment from its kind and
// the AS-name of the first hop in the group that has one. The Cymru verbose
// form is trimmed to its leading token (e.g. "CLOUDFLARENET").
func segmentLabel(kind SegmentKind, hopIdx []int, hops []Hop) string {
	switch kind {
	case SegmentLocal:
		return "Локальная сеть"
	case SegmentUnknown:
		return "Неизвестная сеть"
	}
	asn, asName := firstASN(hopIdx, hops)
	var who string
	switch {
	case asName != "":
		who = shortenASNameLocal(asName)
	case asn != "":
		who = asn
	default:
		who = "?"
	}
	switch kind {
	case SegmentProvider:
		return "Провайдер " + who
	case SegmentDestination, SegmentTransit:
		return who
	}
	return who
}

// segmentHealth scans the hops in a segment for persistent-anomaly flags. The
// first hop with a problem wins — that's where the problem started, which is
// the most useful information for the user.
func segmentHealth(hopIdx []int, hops []Hop) (healthy bool, issue string) {
	for _, i := range hopIdx {
		h := hops[i]
		if h.LossPersists {
			return false, fmt.Sprintf("потери %.0f%% начиная с хопа %d", h.LossPct, h.TTL)
		}
		if h.RTTPersists && h.DeltaRTT > 0 {
			return false, fmt.Sprintf("скачок задержки +%.0f ms на хопе %d", msVal(h.DeltaRTT), h.TTL)
		}
	}
	return true, ""
}

func firstASN(hopIdx []int, hops []Hop) (asn, name string) {
	for _, i := range hopIdx {
		if hops[i].ASN != "" {
			return hops[i].ASN, hops[i].ASName
		}
	}
	return "", ""
}

// shortenASNameLocal trims Team Cymru's verbose form to the leading token.
// Duplicated from the UI helper to keep internal/probe free of UI imports.
func shortenASNameLocal(name string) string {
	if i := strings.Index(name, " - "); i > 0 {
		return name[:i]
	}
	if i := strings.Index(name, ","); i > 0 {
		return name[:i]
	}
	return name
}
