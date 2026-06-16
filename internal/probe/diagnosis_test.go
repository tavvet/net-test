package probe

import (
	"strings"
	"testing"
	"time"
)

// mkh builds a Hop with the fields BuildDiagnosis actually reads.
func mkh(ttl int, ip, asn, asName string, lossPct float64) Hop {
	return Hop{TTL: ttl, IP: ip, ASN: asn, ASName: asName, LossPct: lossPct}
}

func TestBuildDiagnosis_Empty(t *testing.T) {
	d := BuildDiagnosis(nil)
	if len(d.Segments) != 0 {
		t.Errorf("empty hops → %d segments, want 0", len(d.Segments))
	}
}

func TestBuildDiagnosis_TypicalRoute(t *testing.T) {
	// LAN → ISP (one AS, 2 hops) → Cloudflare (2 hops).
	hops := []Hop{
		mkh(1, "10.0.0.1", "", "", 0),
		mkh(2, "5.180.172.2", "AS57043", "ITNET-AS, NL", 0),
		mkh(3, "5.180.172.10", "AS57043", "ITNET-AS, NL", 0),
		mkh(4, "162.158.236.14", "AS13335", "CLOUDFLARENET - Cloudflare, Inc., US", 0),
		mkh(5, "1.1.1.1", "AS13335", "CLOUDFLARENET - Cloudflare, Inc., US", 0),
	}
	d := BuildDiagnosis(hops)
	if len(d.Segments) != 3 {
		t.Fatalf("got %d segments, want 3 (LAN, provider, destination): %+v", len(d.Segments), d.Segments)
	}
	want := []struct {
		kind  SegmentKind
		label string
		from  int
		to    int
	}{
		{SegmentLocal, "Локальная сеть", 1, 1},
		{SegmentProvider, "Провайдер ITNET-AS", 2, 3},
		{SegmentDestination, "CLOUDFLARENET", 4, 5},
	}
	for i, w := range want {
		s := d.Segments[i]
		if s.Kind != w.kind || s.Label != w.label || s.HopFrom != w.from || s.HopTo != w.to {
			t.Errorf("segment %d = %+v, want kind=%v label=%q from=%d to=%d", i, s, w.kind, w.label, w.from, w.to)
		}
		if !s.Healthy {
			t.Errorf("segment %d: Healthy = false, want true (no anomalies)", i)
		}
	}
}

func TestBuildDiagnosis_SilentHopExtendsSegment(t *testing.T) {
	// A silent hop in the middle of the ISP segment must NOT start a new
	// segment — otherwise one unresponsive router fragments the route.
	hops := []Hop{
		mkh(1, "10.0.0.1", "", "", 0),
		mkh(2, "5.180.172.2", "AS57043", "ITNET-AS", 0),
		mkh(3, "", "", "", 100), // silent
		mkh(4, "5.180.172.10", "AS57043", "ITNET-AS", 0),
		mkh(5, "1.1.1.1", "AS13335", "CLOUDFLARENET", 0),
	}
	d := BuildDiagnosis(hops)
	if len(d.Segments) != 3 {
		t.Fatalf("silent hop fragmented route into %d segments, want 3: %+v", len(d.Segments), d.Segments)
	}
	if d.Segments[1].HopTo != 4 || d.Segments[1].HopCount != 3 {
		t.Errorf("ISP segment didn't absorb silent hop: %+v", d.Segments[1])
	}
}

func TestBuildDiagnosis_PersistentLossPointsAtProvider(t *testing.T) {
	hops := []Hop{
		mkh(1, "10.0.0.1", "", "", 0),
		{TTL: 2, IP: "5.180.172.2", ASN: "AS57043", ASName: "ITNET-AS", LossPct: 12, LossPersists: true},
		{TTL: 3, IP: "5.180.172.10", ASN: "AS57043", ASName: "ITNET-AS", LossPct: 12, LossPersists: true},
		{TTL: 4, IP: "1.1.1.1", ASN: "AS13335", ASName: "CLOUDFLARENET", LossPct: 12, LossPersists: true},
	}
	d := BuildDiagnosis(hops)
	if len(d.Segments) != 3 {
		t.Fatalf("got %d segments, want 3", len(d.Segments))
	}
	if d.Segments[0].Healthy != true {
		t.Errorf("LAN segment should be healthy, got %+v", d.Segments[0])
	}
	if d.Segments[1].Healthy != false || !strings.Contains(d.Segments[1].Issue, "потери") {
		t.Errorf("provider segment should report loss, got %+v", d.Segments[1])
	}
	// Cloudflare segment also has LossPersists hops, so it's also flagged —
	// that's correct: the loss continues all the way down.
	if d.Segments[2].Healthy {
		t.Errorf("destination segment should also be flagged (loss continues), got %+v", d.Segments[2])
	}
}

func TestBuildDiagnosis_RTTPersistPointsAtTransit(t *testing.T) {
	hops := []Hop{
		mkh(1, "10.0.0.1", "", "", 0),
		mkh(2, "5.180.172.2", "AS57043", "ITNET-AS", 0),
		{TTL: 3, IP: "162.158.236.14", ASN: "AS13335", ASName: "CLOUDFLARENET", RTTPersists: true, DeltaRTT: 40 * time.Millisecond},
		mkh(4, "1.1.1.1", "AS13335", "CLOUDFLARENET", 0),
	}
	d := BuildDiagnosis(hops)
	if len(d.Segments) != 3 {
		t.Fatalf("got %d segments, want 3: %+v", len(d.Segments), d.Segments)
	}
	if d.Segments[1].Healthy != true {
		t.Errorf("provider should be healthy, got %+v", d.Segments[1])
	}
	if d.Segments[2].Healthy != false || !strings.Contains(d.Segments[2].Issue, "+40") {
		t.Errorf("destination segment should report RTT spike, got %+v", d.Segments[2])
	}
}

func TestBuildDiagnosis_NoASNYet(t *testing.T) {
	// Public hop without ASN info → classified as "Неизвестная сеть"; ISP
	// after it remains a provider, since "?" is a public zone (not local).
	hops := []Hop{
		mkh(1, "10.0.0.1", "", "", 0),
		mkh(2, "5.180.172.2", "", "", 0), // public IP, ASN not yet resolved
		mkh(3, "1.1.1.1", "AS13335", "CLOUDFLARENET", 0),
	}
	d := BuildDiagnosis(hops)
	if len(d.Segments) != 3 {
		t.Fatalf("got %d segments, want 3: %+v", len(d.Segments), d.Segments)
	}
	if d.Segments[1].Kind != SegmentUnknown || d.Segments[1].Label != "Неизвестная сеть" {
		t.Errorf("segment 1 should be unknown, got %+v", d.Segments[1])
	}
}

func TestBuildDiagnosis_HealthyField(t *testing.T) {
	healthy := []Hop{
		mkh(1, "10.0.0.1", "", "", 0),
		mkh(2, "1.1.1.1", "AS13335", "CLOUDFLARENET", 0),
	}
	d := BuildDiagnosis(healthy)
	if !d.Healthy || d.FirstIssue != "" {
		t.Errorf("healthy route: Healthy=%v FirstIssue=%q, want true/empty", d.Healthy, d.FirstIssue)
	}

	bad := []Hop{
		mkh(1, "10.0.0.1", "", "", 0),
		{TTL: 2, IP: "5.180.172.2", ASN: "AS57043", ASName: "ITNET-AS", LossPct: 12, LossPersists: true},
		{TTL: 3, IP: "1.1.1.1", ASN: "AS13335", ASName: "CLOUDFLARENET", LossPct: 12, LossPersists: true},
	}
	d = BuildDiagnosis(bad)
	if d.Healthy {
		t.Errorf("route with loss: Healthy=true, want false")
	}
	if d.FirstIssue != "Провайдер ITNET-AS" {
		t.Errorf("FirstIssue=%q, want first unhealthy segment label", d.FirstIssue)
	}
}

func TestBuildDiagnosis_OnlyLocal(t *testing.T) {
	// Trace that never leaves the LAN — one segment.
	hops := []Hop{
		mkh(1, "10.0.0.1", "", "", 0),
		mkh(2, "192.168.1.1", "", "", 0),
	}
	d := BuildDiagnosis(hops)
	if len(d.Segments) != 1 || d.Segments[0].Kind != SegmentLocal {
		t.Errorf("LAN-only trace: got %+v, want single local segment", d.Segments)
	}
}
