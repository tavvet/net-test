package probe

import (
	"context"
	"math"
	"net"
	"strings"
	"sync"
	"time"
)

// Hop is the accumulated state of one position along the route to the target.
type Hop struct {
	TTL      int
	IP       string // "" if this hop has never replied (shown as "*")
	Host     string // reverse-DNS name, filled in lazily; "" until resolved
	Sent     int
	Recv     int
	LossPct  float64
	LastRTT  time.Duration
	BestRTT  time.Duration
	WorstRTT time.Duration
	AvgRTT   time.Duration
	StdDev   time.Duration

	// Persistent-anomaly markers, filled by markAnomalies. A hop is flagged
	// only when the issue continues to the end of the route (real problem),
	// not when it appears on one hop only (usually ICMP rate-limiting).
	DeltaRTT     time.Duration // AvgRTT − AvgRTT of previous hop; 0 if undefined
	LossPersists bool          // significant loss that propagates down the route
	RTTPersists  bool          // RTT rise that does not recover on later hops

	// ASN enrichment, filled in the background by asnCache. Empty for private
	// IPs (LAN) and until the Team Cymru DNS lookup returns.
	ASN    string // "AS13335", or "" if unknown / private / pending
	ASName string // "CLOUDFLARENET - Cloudflare, Inc., US"
}

// TraceSnapshot is the full per-hop table as of the latest probing cycle.
type TraceSnapshot struct {
	Target    string
	IP        string
	Hops      []Hop
	Diagnosis Diagnosis // per-segment summary; filled by BuildDiagnosis
	Err       string
}

// Tracer repeatedly probes every hop on the path (mtr style) and emits a
// refreshed TraceSnapshot after each cycle.
type Tracer struct {
	target   string
	ip       net.IP
	maxHops  int
	interval time.Duration
	timeout  time.Duration // read window per cycle
	dns      *dnsCache
	asn      *asnCache
}

// NewTracer builds a Tracer for an already-resolved target.
func NewTracer(ip net.IP, label string, maxHops int, interval, timeout time.Duration) *Tracer {
	return &Tracer{
		target:   label,
		ip:       ip,
		maxHops:  maxHops,
		interval: interval,
		timeout:  timeout,
		dns:      newDNSCache(),
		asn:      newASNCache(),
	}
}

type hopAcc struct {
	ip                string
	sent, recv        int
	sum, sumSq        float64 // ms, for avg/stddev
	best, worst, last time.Duration
}

// Run traces until ctx is cancelled.
func (tr *Tracer) Run(ctx context.Context, out chan<- TraceSnapshot) {
	pool, err := newProberPool(min(tr.maxHops, traceConcurrency))
	if err != nil {
		emit(ctx, out, TraceSnapshot{Target: tr.target, IP: tr.ip.String(), Err: err.Error()})
		return
	}
	defer pool.close()

	accs := make([]hopAcc, tr.maxHops+1) // 1-indexed by TTL
	upTo := tr.maxHops                   // probe TTL 1..upTo; shrinks once target found
	foundTTL := 0                        // smallest TTL that reached the target
	cycle := 0

	for {
		cycle++
		// --- probe every hop concurrently, one prober per worker ---
		results := pool.sweep(tr.ip, upTo, cycle, tr.timeout)
		for ttl := 1; ttl <= upTo; ttl++ {
			accs[ttl].sent++
			r := results[ttl]
			if !r.ok {
				continue
			}
			a := &accs[ttl]
			a.recv++
			if r.res.peer != nil {
				a.ip = r.res.peer.String()
			}
			ms := float64(r.res.rtt) / float64(time.Millisecond)
			a.sum += ms
			a.sumSq += ms * ms
			a.last = r.res.rtt
			if a.best == 0 || r.res.rtt < a.best {
				a.best = r.res.rtt
			}
			if r.res.rtt > a.worst {
				a.worst = r.res.rtt
			}
			if r.res.kind == replyEcho && (foundTTL == 0 || ttl < foundTTL) {
				foundTTL = ttl
			}
		}
		if foundTTL > 0 {
			upTo = foundTTL // lock the route length to the target
		}

		// --- build snapshot ---
		hops := make([]Hop, 0, upTo)
		for ttl := 1; ttl <= upTo; ttl++ {
			a := accs[ttl]
			h := Hop{TTL: ttl, IP: a.ip, Sent: a.sent, Recv: a.recv, LastRTT: a.last, BestRTT: a.best, WorstRTT: a.worst}
			if a.sent > 0 {
				h.LossPct = float64(a.sent-a.recv) / float64(a.sent) * 100
			}
			if a.recv > 0 {
				avg := a.sum / float64(a.recv)
				h.AvgRTT = time.Duration(avg * float64(time.Millisecond))
				variance := a.sumSq/float64(a.recv) - avg*avg
				if variance < 0 {
					variance = 0
				}
				h.StdDev = time.Duration(math.Sqrt(variance) * float64(time.Millisecond))
			}
			if a.ip != "" {
				h.Host = tr.dns.lookup(a.ip)
				info := tr.asn.lookup(a.ip)
				h.ASN, h.ASName = info.num, info.name
			}
			hops = append(hops, h)
		}
		markAnomalies(hops)
		snap := TraceSnapshot{
			Target:    tr.target,
			IP:        tr.ip.String(),
			Hops:      hops,
			Diagnosis: BuildDiagnosis(hops),
		}
		if !emit(ctx, out, snap) {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(tr.interval):
		}
	}
}

// traceConcurrency caps how many hops are probed simultaneously per cycle.
const traceConcurrency = 16

// proberPool is a fixed set of probers reused across trace cycles. Each prober
// is touched by exactly one worker goroutine at a time, so no locking is needed.
type proberPool struct {
	probers []prober
}

func newProberPool(n int) (*proberPool, error) {
	if n < 1 {
		n = 1
	}
	p := &proberPool{}
	for range n {
		pr, err := newProber()
		if err != nil {
			p.close()
			return nil, err
		}
		p.probers = append(p.probers, pr)
	}
	return p, nil
}

func (p *proberPool) close() {
	for _, pr := range p.probers {
		pr.close()
	}
}

type sweepResult struct {
	res probeResult
	ok  bool
}

// sweep probes TTL 1..upTo concurrently and returns results indexed by TTL
// ([0] is unused). Workers pull TTLs off a channel, so each TTL is handled once
// and each goroutine writes a distinct slice index — no data race.
func (p *proberPool) sweep(dst net.IP, upTo, cycle int, timeout time.Duration) []sweepResult {
	results := make([]sweepResult, upTo+1)
	ttlCh := make(chan int, upTo)
	for ttl := 1; ttl <= upTo; ttl++ {
		ttlCh <- ttl
	}
	close(ttlCh)
	base := (cycle * 256) & 0xffff // vary seq per cycle so stale replies don't match
	var wg sync.WaitGroup
	for _, pr := range p.probers {
		wg.Go(func() {
			for ttl := range ttlCh {
				seq := (base + ttl) & 0xffff
				res, ok, _ := pr.probe(dst, ttl, seq, timeout)
				results[ttl] = sweepResult{res: res, ok: ok}
			}
		})
	}
	wg.Wait()
	return results
}

// dnsCache resolves hop IPs to names in the background so probing never blocks.
type dnsCache struct {
	mu    sync.Mutex
	names map[string]string
	pend  map[string]struct{}
}

func newDNSCache() *dnsCache {
	return &dnsCache{names: map[string]string{}, pend: map[string]struct{}{}}
}

// lookup returns the cached name for ip, kicking off async resolution on first miss.
func (d *dnsCache) lookup(ip string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n, ok := d.names[ip]; ok {
		return n
	}
	if _, busy := d.pend[ip]; !busy {
		d.pend[ip] = struct{}{}
		go d.resolve(ip)
	}
	return ""
}

func (d *dnsCache) resolve(ip string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	name := ""
	if names, err := net.DefaultResolver.LookupAddr(ctx, ip); err == nil && len(names) > 0 {
		name = strings.TrimSuffix(names[0], ".")
	}
	d.mu.Lock()
	d.names[ip] = name
	d.mu.Unlock()
}
