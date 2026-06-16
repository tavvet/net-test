package probe

import (
	"context"
	"fmt"
	"math"
	"net"
	"time"
)

// Resolve turns a host or IP string into an IPv4 address plus a display label.
func Resolve(host string) (net.IP, string, error) {
	a, err := net.ResolveIPAddr("ip4", host)
	if err != nil {
		return nil, host, fmt.Errorf("не удалось разрешить %q: %w", host, err)
	}
	label := host
	if a.IP.String() != host {
		label = fmt.Sprintf("%s (%s)", host, a.IP)
	}
	return a.IP, label, nil
}

// PingStats is a snapshot of the running ping monitor, emitted once per probe.
type PingStats struct {
	Target   string
	IP       string
	Sent     int
	Recv     int
	LossPct  float64
	LastRTT  time.Duration
	BestRTT  time.Duration
	WorstRTT time.Duration
	AvgRTT   time.Duration
	Jitter   time.Duration // RFC 3550 inter-arrival estimate
	History  []float64     // recent RTTs in ms; 0 marks a lost probe
	Err      string
}

// Pinger continuously pings a single target and emits a PingStats after each probe.
type Pinger struct {
	target   string
	ip       net.IP
	interval time.Duration
	timeout  time.Duration
	histLen  int
}

// NewPinger builds a Pinger for an already-resolved target.
func NewPinger(ip net.IP, label string, interval, timeout time.Duration) *Pinger {
	return &Pinger{target: label, ip: ip, interval: interval, timeout: timeout, histLen: 120}
}

// Run pings until ctx is cancelled, sending a snapshot on out after every probe.
func (pg *Pinger) Run(ctx context.Context, out chan<- PingStats) {
	pr, err := newProber()
	if err != nil {
		emit(ctx, out, PingStats{Target: pg.target, IP: pg.ip.String(), Err: err.Error()})
		return
	}
	defer pr.close()

	var (
		sent, recv        int
		sumRTT            float64 // ms
		best, worst, last time.Duration
		jitter, prevRTT   float64 // ms
		havePrev          bool
		hist              = make([]float64, 0, pg.histLen)
		seq               int
	)

	tick := time.NewTicker(pg.interval)
	defer tick.Stop()

	for {
		seq = (seq + 1) & 0xffff
		sent++
		// TTL 64: we only expect an echo reply from the target, never a hop.
		res, ok, _ := pr.probe(pg.ip, 64, seq, pg.timeout)
		if gotReply := ok && res.kind == replyEcho; gotReply {
			recv++
			rtt := res.rtt
			ms := float64(rtt) / float64(time.Millisecond)
			sumRTT += ms
			last = rtt
			if best == 0 || rtt < best {
				best = rtt
			}
			if rtt > worst {
				worst = rtt
			}
			if havePrev {
				d := math.Abs(ms - prevRTT)
				jitter += (d - jitter) / 16
			}
			prevRTT, havePrev = ms, true
			hist = append(hist, ms)
		} else {
			hist = append(hist, 0) // loss marker
		}
		if len(hist) > pg.histLen {
			hist = hist[len(hist)-pg.histLen:]
		}

		var avg time.Duration
		if recv > 0 {
			avg = time.Duration(sumRTT / float64(recv) * float64(time.Millisecond))
		}
		snap := PingStats{
			Target:   pg.target,
			IP:       pg.ip.String(),
			Sent:     sent,
			Recv:     recv,
			LossPct:  float64(sent-recv) / float64(sent) * 100,
			LastRTT:  last,
			BestRTT:  best,
			WorstRTT: worst,
			AvgRTT:   avg,
			Jitter:   time.Duration(jitter * float64(time.Millisecond)),
			History:  append([]float64(nil), hist...), // copy: UI reads concurrently
		}
		if !emit(ctx, out, snap) {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// emit sends a snapshot unless ctx is done; returns false if the run should stop.
func emit[T any](ctx context.Context, out chan<- T, v T) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- v:
		return true
	}
}
