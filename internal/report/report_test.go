package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tavvet/net-test/internal/probe"
)

func sampleOpts() Options {
	return Options{
		Target:      "1.1.1.1",
		IP:          "1.1.1.1",
		Version:     "test",
		GeneratedAt: time.Date(2026, 6, 16, 18, 0, 0, 0, time.UTC),
		Duration:    10 * time.Second,
	}
}

func samplePing() *probe.PingStats {
	return &probe.PingStats{
		Sent: 12, Recv: 12,
		LossPct:  0,
		AvgRTT:   15200 * time.Microsecond,
		BestRTT:  12 * time.Millisecond,
		WorstRTT: 19 * time.Millisecond,
		Jitter:   1100 * time.Microsecond,
	}
}

func sampleTrace() *probe.TraceSnapshot {
	hops := []probe.Hop{
		{TTL: 1, IP: "10.0.0.1", Host: "router.lan", Sent: 10, Recv: 10, AvgRTT: 1 * time.Millisecond},
		{TTL: 2, IP: "5.180.172.2", ASN: "AS57043", ASName: "ITNET-AS, NL", Sent: 10, Recv: 8, LossPct: 20, AvgRTT: 12 * time.Millisecond, LossPersists: true},
		{TTL: 3, IP: "1.1.1.1", Host: "one.one.one.one", ASN: "AS13335", ASName: "CLOUDFLARENET - Cloudflare, Inc., US", Sent: 10, Recv: 8, LossPct: 20, AvgRTT: 14 * time.Millisecond, LossPersists: true},
	}
	return &probe.TraceSnapshot{
		Target:    "1.1.1.1",
		IP:        "1.1.1.1",
		Hops:      hops,
		Diagnosis: probe.BuildDiagnosis(hops),
	}
}

func sampleSpeed() *probe.SpeedProgress {
	return &probe.SpeedProgress{
		Phase: probe.PhaseDone, Server: "MXP IT", IP: "5.1.2.3",
		LatencyMs: 79.0, JitterMs: 1.1,
		DownloadMbps: 124.0, UploadMbps: 56.0,
	}
}

func TestBuild_AllSections(t *testing.T) {
	r := Build(sampleOpts(), samplePing(), sampleTrace(), sampleSpeed())
	if r.Ping == nil || r.Trace == nil || r.Speed == nil {
		t.Fatalf("missing sections: ping=%v trace=%v speed=%v", r.Ping, r.Trace, r.Speed)
	}
	if r.Ping.Sent != 12 || r.Ping.AvgMs != 15.2 {
		t.Errorf("ping data wrong: %+v", r.Ping)
	}
	if len(r.Trace.Hops) != 3 {
		t.Errorf("hops = %d, want 3", len(r.Trace.Hops))
	}
	if len(r.Trace.Diagnosis) != 3 {
		t.Errorf("diagnosis segments = %d, want 3", len(r.Trace.Diagnosis))
	}
	if r.Trace.Hops[1].ASN != "AS57043" {
		t.Errorf("hop[1].ASN = %q, want AS57043", r.Trace.Hops[1].ASN)
	}
	if r.Speed.DownloadMbps != 124.0 {
		t.Errorf("download = %v, want 124.0", r.Speed.DownloadMbps)
	}
}

func TestBuild_OmitsMissingSections(t *testing.T) {
	r := Build(sampleOpts(), nil, nil, nil)
	if r.Ping != nil || r.Trace != nil || r.Speed != nil {
		t.Errorf("nil inputs should produce nil sections: %+v", r)
	}
}

func TestBuild_OmitsUnfinishedSpeed(t *testing.T) {
	s := &probe.SpeedProgress{Phase: probe.PhaseDownload, DownloadMbps: 50}
	r := Build(sampleOpts(), nil, nil, s)
	if r.Speed != nil {
		t.Errorf("speed not in PhaseDone should be omitted, got %+v", r.Speed)
	}
}

func TestWriteJSON_StableSchema(t *testing.T) {
	r := Build(sampleOpts(), samplePing(), sampleTrace(), sampleSpeed())
	var buf bytes.Buffer
	if err := WriteJSON(&buf, r); err != nil {
		t.Fatal(err)
	}

	// Round-trip through a generic decoder and check the contract field names —
	// these are the public surface and changing them is a breaking change.
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"tool", "version", "target", "ip", "duration_ms", "ping", "trace", "speed"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing top-level key %q", key)
		}
	}
	trace := m["trace"].(map[string]any)
	for _, key := range []string{"hops", "diagnosis"} {
		if _, ok := trace[key]; !ok {
			t.Errorf("missing trace.%s", key)
		}
	}
	hop0 := trace["hops"].([]any)[0].(map[string]any)
	for _, key := range []string{"ttl", "ip", "sent", "recv", "loss_pct"} {
		if _, ok := hop0[key]; !ok {
			t.Errorf("missing hop.%s", key)
		}
	}
}

func TestWriteText_HumanReadable(t *testing.T) {
	r := Build(sampleOpts(), samplePing(), sampleTrace(), sampleSpeed())
	var buf bytes.Buffer
	if err := WriteText(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"net-test test", "цель: 1.1.1.1", "Пинг (12 проб)",
		"средний RTT: 15.2 ms", "Маршрут (3 хопов)",
		"Диагноз:", "Cloudflare", "загрузка ↓: 124.0 Mbps",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text report missing %q", want)
		}
	}
}

func TestDurMs_Rounding(t *testing.T) {
	cases := map[time.Duration]float64{
		0:                        0,
		15234 * time.Microsecond: 15.2, // round to 1 decimal
		1500 * time.Microsecond:  1.5,
		999 * time.Microsecond:   1.0,
	}
	for in, want := range cases {
		if got := durMs(in); got != want {
			t.Errorf("durMs(%v) = %v, want %v", in, got, want)
		}
	}
}
