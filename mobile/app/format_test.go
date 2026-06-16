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
		"ping-zero":  pingText(probe.PingStats{}),
		"ping-err":   pingText(probe.PingStats{Err: "socket"}),
		"ping-data":  pingText(probe.PingStats{Sent: 10, Recv: 9, LossPct: 10, LastRTT: 12 * time.Millisecond, WindowSize: 30}),
		"diag-empty": diagText(probe.TraceSnapshot{}),
		"speed-zero": speedText(probe.SpeedProgress{}),
		"speed-err":  speedText(probe.SpeedProgress{Err: "dial"}),
		"hop-star":   hopLine(probe.Hop{TTL: 3}),
		"hop-data":   hopLine(probe.Hop{TTL: 4, IP: "1.1.1.1", Recv: 5, AvgRTT: 20 * time.Millisecond, ASName: "CLOUDFLARENET - Cloudflare, Inc., US", LossPersists: true}),
	}
	for name, got := range cases {
		if strings.TrimSpace(got) == "" {
			t.Errorf("%s: empty output", name)
		}
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
