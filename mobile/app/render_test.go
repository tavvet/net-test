package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/tavvet/net-test/internal/probe"
)

// TestRenderTabs renders the four tabs to PNGs using Fyne's software test
// canvas — no display or GPU needed. It's a visual aid, not an assertion, so it
// only runs when RENDER_PNG is set (keeps `make test`/CI side-effect free):
//
//	RENDER_PNG=1 RENDER_DIR=/tmp/shots go test -run TestRenderTabs .
func TestRenderTabs(t *testing.T) {
	if os.Getenv("RENDER_PNG") == "" {
		t.Skip("set RENDER_PNG=1 to render tab screenshots")
	}
	dir := os.Getenv("RENDER_DIR")
	if dir == "" {
		dir = "."
	}

	test.NewTempApp(t)
	v := newView()
	v.ping.SetText(pingText(samplePing()))
	v.hops = sampleHops()
	v.hopList.Refresh()
	v.diag.SetText(diagText(sampleTrace()))
	v.speed.SetText(speedText(sampleSpeed()))

	w := test.NewWindow(v.root)
	defer w.Close()
	w.Resize(fyne.NewSize(460, 820))

	shot := func(idx int, name string) {
		v.tabs.SelectIndex(idx)
		v.tabs.Refresh()
		img := w.Canvas().Capture()
		out := filepath.Join(dir, name)
		f, err := os.Create(out)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%dx%d)", out, img.Bounds().Dx(), img.Bounds().Dy())
	}
	shot(0, "tab1-ping.png")
	shot(1, "tab2-trace.png")
	shot(2, "tab3-diag.png")
	shot(3, "tab4-speed.png")
}

func samplePing() probe.PingStats {
	us := func(ms float64) time.Duration { return time.Duration(ms * float64(time.Millisecond)) }
	return probe.PingStats{
		Target: "1.1.1.1", IP: "1.1.1.1",
		Sent: 42, Recv: 42, LossPct: 0,
		LastRTT: us(13.4), AvgRTT: us(12.1), BestRTT: us(10.9), WorstRTT: us(30.2),
		Jitter:     us(1.8),
		WindowSize: 30, WindowLossPct: 0, WindowJitter: us(1.2),
	}
}

func sampleHops() []probe.Hop {
	ms := func(f float64) time.Duration { return time.Duration(f * float64(time.Millisecond)) }
	return []probe.Hop{
		{TTL: 1, IP: "10.0.1.1", Host: "router.lan", Sent: 42, Recv: 42, AvgRTT: ms(1.4)},
		{TTL: 2, IP: "5.180.172.2", Sent: 42, Recv: 42, AvgRTT: ms(11.8), ASName: "ITNET-AS, IT"},
		{TTL: 3, IP: "212.237.216.242", Sent: 42, Recv: 34, LossPct: 18, AvgRTT: ms(42.1), ASName: "ITNET-AS, IT"},
		{TTL: 4, IP: "162.158.236.14", Sent: 42, Recv: 34, LossPct: 18, AvgRTT: ms(42.1), ASName: "CLOUDFLARENET - Cloudflare, Inc., US", LossPersists: true},
		{TTL: 5, IP: "1.1.1.1", Host: "one.one.one.one", Sent: 42, Recv: 34, LossPct: 18, AvgRTT: ms(44.0), ASName: "CLOUDFLARENET - Cloudflare, Inc., US", LossPersists: true},
	}
}

func sampleTrace() probe.TraceSnapshot {
	return probe.TraceSnapshot{
		Target: "1.1.1.1", IP: "1.1.1.1", Hops: sampleHops(),
		Diagnosis: probe.Diagnosis{
			Healthy:    false,
			FirstIssue: "CLOUDFLARENET",
			Segments: []probe.Segment{
				{Label: "Локальная сеть", Kind: probe.SegmentLocal, HopFrom: 1, HopTo: 1, HopCount: 1, Healthy: true},
				{Label: "Провайдер ITNET-AS", Kind: probe.SegmentProvider, HopFrom: 2, HopTo: 3, HopCount: 2, Healthy: true},
				{Label: "CLOUDFLARENET", Kind: probe.SegmentDestination, HopFrom: 4, HopTo: 5, HopCount: 2, Healthy: false, Issue: "потери 18% начиная с хопа 4"},
			},
		},
	}
}

func sampleSpeed() probe.SpeedProgress {
	return probe.SpeedProgress{
		Phase: probe.PhaseDone, Server: "MXP (Milan)", IP: "203.0.113.7",
		LatencyMs: 14, JitterMs: 2, DownloadMbps: 187.4, UploadMbps: 42.1,
	}
}
