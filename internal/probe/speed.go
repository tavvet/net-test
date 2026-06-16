package probe

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Cloudflare's open speed-test endpoints. No API key, no install required.
const (
	cfDown  = "https://speed.cloudflare.com/__down?bytes="
	cfUp    = "https://speed.cloudflare.com/__up"
	cfTrace = "https://speed.cloudflare.com/cdn-cgi/trace"

	latencySamples = 14
	latencyWarmup  = 2 // first samples include TLS/TCP setup; excluded from stats
	downWindow     = 8 * time.Second
	downStreams    = 4
	upWindow       = 8 * time.Second
	upStreams      = 3
	chunkBytes     = 50_000_000 // per download request (Cloudflare caps near 100MB); looped to fill the window
)

// SpeedPhase tracks which part of the test is running.
type SpeedPhase int

const (
	PhaseIdle SpeedPhase = iota
	PhaseLatency
	PhaseDownload
	PhaseUpload
	PhaseDone
	PhaseError
)

func (p SpeedPhase) String() string {
	switch p {
	case PhaseLatency:
		return "латентность"
	case PhaseDownload:
		return "загрузка ↓"
	case PhaseUpload:
		return "отдача ↑"
	case PhaseDone:
		return "готово"
	case PhaseError:
		return "ошибка"
	default:
		return "ожидание"
	}
}

// SpeedProgress is streamed throughout the test: live fields update the gauge of
// the current phase, result fields accumulate as phases complete.
type SpeedProgress struct {
	Phase   SpeedPhase
	Mbps    float64 // live throughput of the current phase
	Percent float64 // 0..1 progress of the current phase

	LatencyMs    float64
	JitterMs     float64
	DownloadMbps float64
	UploadMbps   float64
	Server       string // Cloudflare colo / location
	IP           string
	Err          string
}

func speedClient() *http.Client {
	tr := &http.Transport{
		DisableCompression:  true,
		MaxIdleConns:        32,
		MaxConnsPerHost:     32,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{Transport: tr}
}

// RunSpeedTest runs latency → download → upload, streaming SpeedProgress on out.
// It stops early if ctx is cancelled.
func RunSpeedTest(ctx context.Context, out chan<- SpeedProgress) {
	client := speedClient()
	res := SpeedProgress{Phase: PhaseLatency}
	res.Server, res.IP = fetchMeta(ctx, client)
	emit(ctx, out, res)

	// --- latency ---
	lat, jit, err := measureLatency(ctx, client, out, &res)
	if err != nil {
		res.Phase, res.Err = PhaseError, err.Error()
		emit(ctx, out, res)
		return
	}
	res.LatencyMs, res.JitterMs = lat, jit

	// --- download ---
	res.Phase = PhaseDownload
	emit(ctx, out, res)
	res.DownloadMbps = measureStreams(ctx, out, &res, downWindow, downStreams, func(sctx context.Context, add func(int64)) {
		downloadStream(sctx, client, add)
	})

	// --- upload ---
	res.Phase = PhaseUpload
	res.Mbps, res.Percent = 0, 0
	emit(ctx, out, res)
	res.UploadMbps = measureStreams(ctx, out, &res, upWindow, upStreams, func(sctx context.Context, add func(int64)) {
		uploadStream(sctx, client, add)
	})

	res.Phase, res.Mbps, res.Percent = PhaseDone, 0, 1
	emit(ctx, out, res)
}

// fetchMeta reads the Cloudflare datacenter (colo) and client IP for display.
func fetchMeta(ctx context.Context, client *http.Client) (server, ip string) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cfTrace, nil)
	resp, err := client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	colo, loc := "", ""
	for line := range strings.SplitSeq(string(body), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "colo":
			colo = v
		case "loc":
			loc = v
		case "ip":
			ip = v
		}
	}
	server = strings.TrimSpace(colo + " " + loc)
	return server, ip
}

// measureLatency samples TTFB to a zero-byte download a number of times and
// returns average latency and jitter (stddev), both in milliseconds.
func measureLatency(ctx context.Context, client *http.Client, out chan<- SpeedProgress, res *SpeedProgress) (float64, float64, error) {
	var samples []float64
	for i := range latencySamples {
		if ctx.Err() != nil {
			break
		}
		ms, err := ttfb(ctx, client)
		if err != nil {
			if len(samples) == 0 && i >= 2 {
				return 0, 0, fmt.Errorf("speed.cloudflare.com недоступен: %w", err)
			}
			continue
		}
		samples = append(samples, ms)
		avg, jit := meanStd(warm(samples))
		res.LatencyMs, res.JitterMs = avg, jit
		res.Percent = float64(i+1) / float64(latencySamples)
		emit(ctx, out, *res)
	}
	if len(samples) == 0 {
		return 0, 0, fmt.Errorf("не удалось измерить латентность")
	}
	avg, jit := meanStd(warm(samples))
	return avg, jit, nil
}

// warm drops the initial samples whose connection setup inflates the timing.
func warm(samples []float64) []float64 {
	if len(samples) > latencyWarmup {
		return samples[latencyWarmup:]
	}
	return samples
}

func ttfb(ctx context.Context, client *http.Client) (float64, error) {
	var start time.Time
	var firstByte time.Duration
	trace := &httptrace.ClientTrace{
		GotFirstResponseByte: func() { firstByte = time.Since(start) },
	}
	req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, cfDown+"0", nil)
	start = time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return float64(firstByte) / float64(time.Millisecond), nil
}

// measureStreams runs `streams` concurrent transfers for `window`, summing bytes
// via the add callback, while emitting live throughput. Returns final Mbps.
func measureStreams(ctx context.Context, out chan<- SpeedProgress, res *SpeedProgress, window time.Duration, streams int, stream func(context.Context, func(int64))) float64 {
	sctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	var total int64
	add := func(n int64) { atomic.AddInt64(&total, n) }
	start := time.Now()

	var wg sync.WaitGroup
	for range streams {
		wg.Go(func() { stream(sctx, add) })
	}

	done := make(chan struct{})
	tickerDone := make(chan struct{})
	go func() {
		defer close(tickerDone)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				elapsed := time.Since(start).Seconds()
				if elapsed <= 0 {
					continue
				}
				// Emit a local copy: don't mutate *res from here. The finaliser
				// below also writes res.Mbps/res.Percent; with two writers we
				// need full happens-before, which is what tickerDone provides.
				snap := *res
				snap.Mbps = float64(atomic.LoadInt64(&total)) * 8 / elapsed / 1e6
				snap.Percent = math.Min(1, elapsed/window.Seconds())
				emit(ctx, out, snap)
			}
		}
	}()

	wg.Wait()
	close(done)
	<-tickerDone // wait for ticker to fully exit before mutating *res
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		return 0
	}
	mbps := float64(atomic.LoadInt64(&total)) * 8 / elapsed / 1e6
	res.Mbps, res.Percent = mbps, 1
	emit(ctx, out, *res)
	return mbps
}

func downloadStream(ctx context.Context, client *http.Client, add func(int64)) {
	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s%d", cfDown, chunkBytes), nil)
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return // avoid hot-looping on a rejected request
		}
		buf := make([]byte, 64*1024)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				add(int64(n))
			}
			if rerr != nil {
				break
			}
		}
		resp.Body.Close()
	}
}

func uploadStream(ctx context.Context, client *http.Client, add func(int64)) {
	for ctx.Err() == nil {
		body := &uploadReader{ctx: ctx, buf: make([]byte, 64*1024), add: add}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, cfUp, body)
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// uploadReader feeds an endless stream of bytes to a POST, counting what it hands
// off, until the phase context is cancelled.
type uploadReader struct {
	ctx context.Context
	buf []byte
	add func(int64)
}

func (u *uploadReader) Read(p []byte) (int, error) {
	if u.ctx.Err() != nil {
		return 0, io.EOF
	}
	n := copy(p, u.buf)
	u.add(int64(n))
	return n, nil
}

func meanStd(xs []float64) (mean, std float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean = sum / float64(len(xs))
	var v float64
	for _, x := range xs {
		v += (x - mean) * (x - mean)
	}
	std = math.Sqrt(v / float64(len(xs)))
	return mean, std
}
