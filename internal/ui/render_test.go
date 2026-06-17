package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tavvet/net-test/internal/probe"
)

// sampleModel builds a model with representative data for render smoke tests.
func sampleModel() model {
	m := New(context.TODO(), "1.1.1.1", "1.1.1.1", Channels{}, time.Now()).(model)
	m.w, m.h = 140, 30
	m.havePing = true
	m.ping = probe.PingStats{
		Target: "1.1.1.1", IP: "1.1.1.1", Sent: 20, Recv: 19, LossPct: 5,
		LastRTT: 14 * time.Millisecond, AvgRTT: 15 * time.Millisecond,
		BestRTT: 10 * time.Millisecond, WorstRTT: 40 * time.Millisecond,
		Jitter:  2 * time.Millisecond,
		History: []float64{12, 14, 0, 16, 18, 13, 200, 14},
		// Rolling-window: enough samples for a verdict; 5% loss → "Плохо".
		WindowSize:    20,
		WindowLossPct: 5,
		WindowJitter:  2 * time.Millisecond,
	}
	m.haveTrace = true
	m.trace = probe.TraceSnapshot{
		Target: "1.1.1.1", IP: "1.1.1.1",
		Hops: []probe.Hop{
			{TTL: 1, IP: "10.0.1.1", Host: "router.lan", Sent: 10, Recv: 10, LastRTT: 1 * time.Millisecond, AvgRTT: 1 * time.Millisecond}, // private → "локальная сеть"
			// hop 2: 20% loss, but later hops clean → transient, must NOT show flag
			{TTL: 2, IP: "5.180.172.2", Sent: 10, Recv: 8, LossPct: 20, LastRTT: 12 * time.Millisecond, AvgRTT: 12 * time.Millisecond, ASN: "AS57043", ASName: "ITNET-AS, NL"},
			{TTL: 3, IP: "212.237.216.242", Sent: 10, Recv: 10, LastRTT: 14 * time.Millisecond, AvgRTT: 14 * time.Millisecond, ASN: "AS5390"},
			// hop 4: persistent loss + RTT rise → MUST show flag and Δ
			{TTL: 4, IP: "162.158.236.14", Sent: 10, Recv: 8, LossPct: 20, LastRTT: 50 * time.Millisecond, AvgRTT: 50 * time.Millisecond, WorstRTT: 190 * time.Millisecond, StdDev: 38 * time.Millisecond, DeltaRTT: 36 * time.Millisecond, LossPersists: true, RTTPersists: true, ASN: "AS13335", ASName: "CLOUDFLARENET - Cloudflare, Inc., US"},
			{TTL: 5, IP: "1.1.1.1", Host: "one.one.one.one", Sent: 10, Recv: 8, LossPct: 20, LastRTT: 52 * time.Millisecond, AvgRTT: 52 * time.Millisecond, StdDev: 9 * time.Millisecond, LossPersists: true, ASN: "AS13335", ASName: "CLOUDFLARENET - Cloudflare, Inc., US"},
		},
	}
	m.speed = probe.SpeedProgress{
		Phase: probe.PhaseDone, Server: "MXP IT", IP: "5.1.2.3",
		LatencyMs: 12.3, JitterMs: 1.1, DownloadMbps: 432.1, UploadMbps: 88.8,
	}
	return m
}

func TestViewRendersAllTabs(t *testing.T) {
	m := sampleModel()
	for _, tb := range []tab{tabPing, tabTrace, tabDiagnosis, tabSpeed} {
		m.tab = tb
		out := m.View()
		if strings.TrimSpace(out) == "" {
			t.Fatalf("tab %d rendered empty", tb)
		}
	}
}

func TestViewDiagnosisContent(t *testing.T) {
	m := sampleModel()
	// Attach a diagnosis to the sample so the tab has something to render.
	m.trace.Diagnosis = probe.BuildDiagnosis(m.trace.Hops)
	m.tab = tabDiagnosis
	out := m.View()
	for _, want := range []string{"Маршрут до", "Локальная сеть", "CLOUDFLARENET", "⚠", "проблема в зоне"} {
		if !strings.Contains(out, want) {
			t.Errorf("diagnosis view missing %q", want)
		}
	}
}

func TestViewPingContent(t *testing.T) {
	m := sampleModel()
	m.tab = tabPing
	out := m.View()
	for _, want := range []string{"Качество", "RTT", "Потери"} {
		if !strings.Contains(out, want) {
			t.Errorf("ping view missing %q", want)
		}
	}
}

func TestViewPingCollectingWhenWindowTooSmall(t *testing.T) {
	m := sampleModel()
	m.ping.WindowSize = 3
	m.ping.WindowLossPct = 33
	m.tab = tabPing
	out := m.View()
	if !strings.Contains(out, "Собираю данные") {
		t.Errorf("expected collecting placeholder for small window; got: %s", out)
	}
	// Even with high WindowLossPct, the user must NOT see "Плохо/Критично" yet.
	for _, bad := range []string{"Плохо", "Критично", "Хорошо"} {
		if strings.Contains(out, bad) {
			t.Errorf("verdict %q leaked while window is below MinVerdictSamples", bad)
		}
	}
}

func TestViewTraceContent(t *testing.T) {
	m := sampleModel()
	m.trace.Diagnosis = probe.BuildDiagnosis(m.trace.Hops)
	m.tab = tabTrace
	out := m.View()
	// "⚠" + "+36": persistent anomaly with Δ suffix.
	// "локальная сеть": private hop labelled.
	// "CLOUDFLARENET": AS name shortened (full form has " - Cloudflare, …").
	// "начинаются на хопе 4": the route headline points at the first persistent hop.
	for _, want := range []string{"Хост", "10.0.1.1", "1.1.1.1", "⚠", "+36", "локальная сеть", "CLOUDFLARENET", "начинаются на хопе 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("trace view missing %q", want)
		}
	}
	// The full Cymru form must NOT leak — shortenASName should trim it.
	if strings.Contains(out, "Cloudflare, Inc.") {
		t.Errorf("AS name not shortened — full Cymru form appears in output")
	}
}

func TestRouteHeadline(t *testing.T) {
	if got, _ := routeHeadline(probe.Diagnosis{}); got != "" {
		t.Errorf("no route yet → %q, want empty (no banner)", got)
	}
	healthy := probe.Diagnosis{Healthy: true, Segments: []probe.Segment{{Healthy: true}}}
	if got, _ := routeHeadline(healthy); !strings.Contains(got, "нет") {
		t.Errorf("healthy → %q, want all-clear", got)
	}
	loss := probe.Diagnosis{
		Segments:       []probe.Segment{{Healthy: false}},
		FirstIssue:     "CLOUDFLARENET",
		FirstIssueHop:  4,
		FirstIssueLoss: true,
	}
	if got, _ := routeHeadline(loss); !strings.Contains(got, "Потери") || !strings.Contains(got, "хопе 4") || !strings.Contains(got, "CLOUDFLARENET") {
		t.Errorf("loss headline = %q, want loss + hop 4 + zone", got)
	}
	lat := probe.Diagnosis{
		Segments:      []probe.Segment{{Healthy: false}},
		FirstIssue:    "ITNET-AS",
		FirstIssueHop: 2,
	}
	if got, _ := routeHeadline(lat); !strings.Contains(got, "задержк") || !strings.Contains(got, "хопе 2") {
		t.Errorf("latency headline = %q, want latency + hop 2", got)
	}
}

// TestLossColor locks the route gauge to the SHARED probe.Quality classifier
// (4 levels, 0% neutral) so the TUI and the mobile GUI can't drift apart on how
// they bucket per-hop loss.
func TestLossColor(t *testing.T) {
	if lossColor(0) != cMuted {
		t.Errorf("0%% loss = %v, want neutral grey (cMuted)", lossColor(0))
	}
	if lossColor(0.5) != cOK {
		t.Errorf("0–1%% loss = %v, want QualityGood (cOK)", lossColor(0.5))
	}
	if lossColor(3) != cWarn {
		t.Errorf("1–5%% loss = %v, want QualityBad (cWarn)", lossColor(3))
	}
	if lossColor(10) != cBad {
		t.Errorf(">5%% loss = %v, want QualityCritical (cBad)", lossColor(10))
	}
}

func TestShortenASName(t *testing.T) {
	cases := map[string]string{
		"CLOUDFLARENET - Cloudflare, Inc., US": "CLOUDFLARENET",
		"GOOGLE, US":                           "GOOGLE",
		"PLAIN":                                "PLAIN",
		"":                                     "",
	}
	for in, want := range cases {
		if got := probe.ShortenASName(in); got != want {
			t.Errorf("probe.ShortenASName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSnapshot prints all tabs to stdout (set NETTEST_SNAPSHOT=1) for visual
// inspection of the real layout. It is not an assertion test.
func TestSnapshot(t *testing.T) {
	if os.Getenv("NETTEST_SNAPSHOT") == "" {
		t.Skip("set NETTEST_SNAPSHOT=1 to print frames")
	}
	m := sampleModel()
	m.trace.Diagnosis = probe.BuildDiagnosis(m.trace.Hops)
	for _, tb := range []tab{tabPing, tabTrace, tabDiagnosis, tabSpeed} {
		m.tab = tb
		fmt.Printf("\n========== TAB %d ==========\n%s\n", tb, m.View())
	}
}

func TestVerdict(t *testing.T) {
	cases := []struct {
		loss, jitter float64
		wantLabel    string
		wantReason   string // substring; "" means reason must be empty
	}{
		{0, 2, "Отлично", ""},
		{0, 12, "Хорошо", "джиттер"},
		{0.5, 3, "Хорошо", "потери"},
		{3.2, 5, "Плохо", "потери 3.2%"},
		{0, 25, "Плохо", "джиттер 25 ms"},
		{7, 5, "Критично", "потери 7.0%"},
	}
	for _, c := range cases {
		label, reason, _ := verdict(c.loss, c.jitter)
		if label != c.wantLabel {
			t.Errorf("verdict(%.1f, %.1f) label = %q, want %q", c.loss, c.jitter, label, c.wantLabel)
		}
		if c.wantReason == "" && reason != "" {
			t.Errorf("verdict(%.1f, %.1f) reason = %q, want empty", c.loss, c.jitter, reason)
		}
		if c.wantReason != "" && !strings.Contains(reason, c.wantReason) {
			t.Errorf("verdict(%.1f, %.1f) reason = %q, want contains %q", c.loss, c.jitter, reason, c.wantReason)
		}
	}
}

func TestSparklineLength(t *testing.T) {
	// Loss markers and blocks should each occupy one cell.
	s := sparkline([]float64{10, 0, 20, 30}, 10)
	if s == "" {
		t.Fatal("empty sparkline")
	}
}
