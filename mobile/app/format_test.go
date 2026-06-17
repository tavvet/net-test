package main

import (
	"strings"
	"testing"
	"time"

	"github.com/tavvet/net-test/internal/probe"
)

// The four tab formatters run on every snapshot, including the very first
// (all-zero) one before any probe has replied. They must never panic and must
// always produce something to show.
func TestFormattersHandleEmptyAndPopulated(t *testing.T) {
	cases := map[string]string{
		"ping-zero":     pingText(probe.PingStats{}),
		"ping-err":      pingText(probe.PingStats{Err: "socket"}),
		"ping-data":     pingText(probe.PingStats{Sent: 10, Recv: 9, LossPct: 10, LastRTT: 12 * time.Millisecond, WindowSize: 30}),
		"diag-empty":    diagText(probe.TraceSnapshot{}),
		"speed-zero":    speedText(probe.SpeedProgress{}),
		"speed-err":     speedText(probe.SpeedProgress{Err: "dial"}),
		"hopname-star":  hopName(probe.Hop{TTL: 3}),
		"hopname-ip":    hopName(probe.Hop{IP: "1.1.1.1"}),
		"hoprtt-silent": hopRTT(probe.Hop{TTL: 3}),
		"lossbar-zero":  lossBar(0, 10),
		"lossbar-full":  lossBar(18, 18),
	}
	for name, got := range cases {
		if strings.TrimSpace(got) == "" {
			t.Errorf("%s: empty output", name)
		}
	}
}

func TestRouteHeadline(t *testing.T) {
	if got, _ := routeHeadline(probe.Diagnosis{}); !strings.Contains(got, "Сбор") {
		t.Errorf("empty diagnosis → %q, want collecting placeholder", got)
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

func TestLossBar(t *testing.T) {
	if got := lossBar(0, 10); strings.Count(got, "█") != 0 {
		t.Errorf("0%% loss → %q, want no filled cells", got)
	}
	if got := lossBar(18, 18); strings.Count(got, "█") != 14 {
		t.Errorf("max loss → %q, want a full 14-cell bar", got)
	}
	if got := lossBar(9, 18); len([]rune(got)) != 14 {
		t.Errorf("bar width = %d runes, want 14", len([]rune(got)))
	}
}

func TestDiagTextHealthyVsProblem(t *testing.T) {
	healthy := probe.TraceSnapshot{Diagnosis: probe.Diagnosis{
		Healthy:  true,
		Segments: []probe.Segment{{Label: "Локальная сеть", HopFrom: 1, HopTo: 1, Healthy: true}},
	}}
	if got := diagText(healthy); !strings.Contains(got, "здоров") {
		t.Errorf("healthy diag missing verdict: %q", got)
	}

	problem := probe.TraceSnapshot{Diagnosis: probe.Diagnosis{
		Healthy:    false,
		FirstIssue: "CLOUDFLARENET",
		Segments: []probe.Segment{
			{Label: "Локальная сеть", HopFrom: 1, HopTo: 1, Healthy: true},
			{Label: "CLOUDFLARENET", HopFrom: 4, HopTo: 5, Healthy: false, Issue: "потери 18% с хопа 4"},
		},
	}}
	got := diagText(problem)
	if !strings.Contains(got, "CLOUDFLARENET") || !strings.Contains(got, "→") {
		t.Errorf("problem diag missing zone/issue: %q", got)
	}
}

func TestVerdictWindow(t *testing.T) {
	tests := []struct {
		name string
		p    probe.PingStats
		want string
	}{
		{"too-few-samples", probe.PingStats{WindowSize: 2}, "сбор данных…"},
		{"clean", probe.PingStats{WindowSize: 30, WindowJitter: 2 * time.Millisecond}, "Отлично"},
		{"high-loss", probe.PingStats{WindowSize: 30, WindowLossPct: 8}, "Критично (потери 8.0%)"},
		{"mild-loss", probe.PingStats{WindowSize: 30, WindowLossPct: 2}, "Плохо (потери 2.0%)"},
		{"jitter", probe.PingStats{WindowSize: 30, WindowJitter: 30 * time.Millisecond}, "Плохо (джиттер 30 ms)"},
	}
	for _, tt := range tests {
		if got := verdict(tt.p); got != tt.want {
			t.Errorf("%s: verdict = %q, want %q", tt.name, got, tt.want)
		}
	}
}
