// Package report builds and renders the one-shot output of `net-test --once`.
// It defines a stable wire format (separate from internal probe types) so the
// JSON contract can evolve independently of the measurement layer.
//
// Durations are exposed in milliseconds, not Go's default nanoseconds —
// easier to consume from shell pipelines and provider-ticket attachments.
package report

import (
	"time"

	"github.com/tavvet/net-test/internal/probe"
)

// Report is the full one-shot snapshot of a target's connectivity.
type Report struct {
	GeneratedAt time.Time    `json:"generated_at"`
	Tool        string       `json:"tool"`
	Version     string       `json:"version"`
	Target      string       `json:"target"`
	IP          string       `json:"ip"`
	DurationMs  int64        `json:"duration_ms"`
	Ping        *PingReport  `json:"ping,omitempty"`
	Trace       *TraceReport `json:"trace,omitempty"`
	Speed       *SpeedReport `json:"speed,omitempty"`
}

// PingReport summarises the ping monitor.
type PingReport struct {
	Sent     int     `json:"sent"`
	Recv     int     `json:"recv"`
	LossPct  float64 `json:"loss_pct"`
	AvgMs    float64 `json:"avg_ms"`
	BestMs   float64 `json:"best_ms"`
	WorstMs  float64 `json:"worst_ms"`
	JitterMs float64 `json:"jitter_ms"`
	Err      string  `json:"error,omitempty"` // probe-side failure (e.g. ICMP socket open)
}

// TraceReport is the per-hop route plus its per-segment diagnosis.
type TraceReport struct {
	Hops      []HopReport     `json:"hops,omitempty"`
	Diagnosis []SegmentReport `json:"diagnosis,omitempty"`
	Err       string          `json:"error,omitempty"`
}

// HopReport is one row of the route table. Fields that are typically zero on
// silent or fresh hops are omitempty so the JSON stays readable.
type HopReport struct {
	TTL          int     `json:"ttl"`
	IP           string  `json:"ip,omitempty"`
	Host         string  `json:"host,omitempty"`
	Sent         int     `json:"sent,omitempty"`
	Recv         int     `json:"recv,omitempty"`
	LossPct      float64 `json:"loss_pct,omitempty"`
	AvgMs        float64 `json:"avg_ms,omitempty"`
	BestMs       float64 `json:"best_ms,omitempty"`
	WorstMs      float64 `json:"worst_ms,omitempty"`
	StdDevMs     float64 `json:"stddev_ms,omitempty"`
	DeltaMs      float64 `json:"delta_ms,omitempty"`
	LossPersists bool    `json:"loss_persists,omitempty"`
	RTTPersists  bool    `json:"rtt_persists,omitempty"`
	ASN          string  `json:"asn,omitempty"`
	ASName       string  `json:"as_name,omitempty"`
}

// SegmentReport mirrors probe.Segment with stable JSON field names.
type SegmentReport struct {
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	HopFrom  int    `json:"hop_from"`
	HopTo    int    `json:"hop_to"`
	HopCount int    `json:"hop_count"`
	Healthy  bool   `json:"healthy"`
	Issue    string `json:"issue,omitempty"`
}

// SpeedReport is the throughput summary.
type SpeedReport struct {
	Server       string  `json:"server,omitempty"`
	ClientIP     string  `json:"client_ip,omitempty"`
	LatencyMs    float64 `json:"latency_ms"`
	JitterMs     float64 `json:"jitter_ms"`
	DownloadMbps float64 `json:"download_mbps"`
	UploadMbps   float64 `json:"upload_mbps"`
	Err          string  `json:"error,omitempty"`
}

// Options bundles the inputs that the headless caller has but the wire layer
// can't infer (target label, version string, generation timestamp, etc).
type Options struct {
	Target      string
	IP          string
	Version     string
	GeneratedAt time.Time
	Duration    time.Duration
}

// Build assembles a Report from the latest snapshots of each measurement. Any
// of ping/trace/speed may be nil — those sections will simply be omitted.
func Build(opts Options, ping *probe.PingStats, trace *probe.TraceSnapshot, speed *probe.SpeedProgress) Report {
	r := Report{
		GeneratedAt: opts.GeneratedAt,
		Tool:        "net-test",
		Version:     opts.Version,
		Target:      opts.Target,
		IP:          opts.IP,
		DurationMs:  opts.Duration.Milliseconds(),
	}
	// Each section is emitted if it has DATA or a probe-side ERROR. Silently
	// dropping a failed phase would let a broken `--once --json` look "all OK"
	// to a cron job; the error is now visible in the output.
	if ping != nil && (ping.Sent > 0 || ping.Err != "") {
		r.Ping = pingFrom(*ping)
	}
	if trace != nil && (len(trace.Hops) > 0 || trace.Err != "") {
		r.Trace = traceFrom(*trace)
	}
	if speed != nil && (speed.Phase == probe.PhaseDone || speed.Phase == probe.PhaseError) {
		r.Speed = speedFrom(*speed)
	}
	return r
}

func pingFrom(p probe.PingStats) *PingReport {
	return &PingReport{
		Sent:     p.Sent,
		Recv:     p.Recv,
		LossPct:  round1(p.LossPct),
		AvgMs:    durMs(p.AvgRTT),
		BestMs:   durMs(p.BestRTT),
		WorstMs:  durMs(p.WorstRTT),
		JitterMs: durMs(p.Jitter),
		Err:      p.Err,
	}
}

func traceFrom(t probe.TraceSnapshot) *TraceReport {
	hops := make([]HopReport, len(t.Hops))
	for i, h := range t.Hops {
		hops[i] = hopFrom(h)
	}
	segs := make([]SegmentReport, len(t.Diagnosis.Segments))
	for i, s := range t.Diagnosis.Segments {
		segs[i] = SegmentReport{
			Label:    s.Label,
			Kind:     s.Kind.String(),
			HopFrom:  s.HopFrom,
			HopTo:    s.HopTo,
			HopCount: s.HopCount,
			Healthy:  s.Healthy,
			Issue:    s.Issue,
		}
	}
	return &TraceReport{Hops: hops, Diagnosis: segs, Err: t.Err}
}

func hopFrom(h probe.Hop) HopReport {
	return HopReport{
		TTL:          h.TTL,
		IP:           h.IP,
		Host:         h.Host,
		Sent:         h.Sent,
		Recv:         h.Recv,
		LossPct:      round1(h.LossPct),
		AvgMs:        durMs(h.AvgRTT),
		BestMs:       durMs(h.BestRTT),
		WorstMs:      durMs(h.WorstRTT),
		StdDevMs:     durMs(h.StdDev),
		DeltaMs:      durMs(h.DeltaRTT),
		LossPersists: h.LossPersists,
		RTTPersists:  h.RTTPersists,
		ASN:          h.ASN,
		ASName:       h.ASName,
	}
}

func speedFrom(s probe.SpeedProgress) *SpeedReport {
	return &SpeedReport{
		Server:       s.Server,
		ClientIP:     s.IP,
		LatencyMs:    round1(s.LatencyMs),
		JitterMs:     round1(s.JitterMs),
		DownloadMbps: round1(s.DownloadMbps),
		UploadMbps:   round1(s.UploadMbps),
		Err:          s.Err,
	}
}

// durMs converts a duration to milliseconds rounded to one decimal place.
// Thin wrapper over probe.Millis so the JSON layer owns its own rounding
// policy without duplicating the underlying conversion.
func durMs(d time.Duration) float64 { return round1(probe.Millis(d)) }

func round1(x float64) float64 {
	return float64(int64(x*10+0.5)) / 10
}
