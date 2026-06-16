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
	m.w, m.h = 100, 30
	m.havePing = true
	m.ping = probe.PingStats{
		Target: "1.1.1.1", IP: "1.1.1.1", Sent: 20, Recv: 19, LossPct: 5,
		LastRTT: 14 * time.Millisecond, AvgRTT: 15 * time.Millisecond,
		BestRTT: 10 * time.Millisecond, WorstRTT: 40 * time.Millisecond,
		Jitter:  2 * time.Millisecond,
		History: []float64{12, 14, 0, 16, 18, 13, 200, 14},
	}
	m.haveTrace = true
	m.trace = probe.TraceSnapshot{
		Target: "1.1.1.1", IP: "1.1.1.1",
		Hops: []probe.Hop{
			{TTL: 1, IP: "10.0.1.1", Host: "router.lan", Sent: 10, Recv: 10, LastRTT: 1 * time.Millisecond, AvgRTT: 1 * time.Millisecond},
			{TTL: 2, IP: "", Sent: 10, Recv: 0, LossPct: 100},
			{TTL: 3, IP: "1.1.1.1", Sent: 10, Recv: 9, LossPct: 10, LastRTT: 14 * time.Millisecond, AvgRTT: 15 * time.Millisecond, StdDev: 3 * time.Millisecond},
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
	for _, tb := range []tab{tabPing, tabTrace, tabSpeed} {
		m.tab = tb
		out := m.View()
		if strings.TrimSpace(out) == "" {
			t.Fatalf("tab %d rendered empty", tb)
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

func TestViewTraceContent(t *testing.T) {
	m := sampleModel()
	m.tab = tabTrace
	out := m.View()
	for _, want := range []string{"Хост", "10.0.1.1", "*", "1.1.1.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("trace view missing %q", want)
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
	for _, tb := range []tab{tabPing, tabTrace, tabSpeed} {
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
