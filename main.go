// net-test is a console-GUI utility for checking connection quality: live
// latency/loss/jitter to a target, an mtr-style per-hop route view that shows
// WHERE drops happen, and a Cloudflare-based download/upload speed test.
//
// Without --once it launches the Bubble Tea TUI. With --once it runs a single
// measurement window in the terminal and prints a report (text or JSON) to
// stdout — convenient for ISP tickets and cron-style monitoring.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/tavvet/net-test/internal/probe"
	"github.com/tavvet/net-test/internal/report"
	"github.com/tavvet/net-test/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// version is overridden at build time via -ldflags "-X main.version=…".
var version = "dev"

type options struct {
	target   string
	interval time.Duration
	timeout  time.Duration
	maxHops  int
	once     bool
	jsonOut  bool
	duration time.Duration
	noSpeed  bool
}

func main() {
	var opts options
	flag.StringVar(&opts.target, "target", "1.1.1.1", "хост или IP для проверки соединения")
	flag.DurationVar(&opts.interval, "interval", time.Second, "интервал между пробами (пинг и трассировка)")
	flag.DurationVar(&opts.timeout, "timeout", 2*time.Second, "таймаут ожидания ICMP-ответа")
	flag.IntVar(&opts.maxHops, "max-hops", 30, "максимум хопов для трассировки маршрута")
	flag.BoolVar(&opts.once, "once", false, "один прогон без TUI: отчёт в stdout и выход")
	flag.BoolVar(&opts.jsonOut, "json", false, "JSON-формат отчёта (только с -once)")
	flag.DurationVar(&opts.duration, "duration", 10*time.Second, "окно сбора пинга и трассы для -once")
	flag.BoolVar(&opts.noSpeed, "no-speed", false, "пропустить тест скорости (только с -once)")
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	flag.Parse()

	if *showVersion {
		fmt.Println("net-test", version)
		return
	}

	ip, label, err := probe.Resolve(opts.target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	if opts.once {
		if err := runHeadless(ip, label, opts); err != nil {
			fmt.Fprintln(os.Stderr, "Ошибка:", err)
			os.Exit(1)
		}
		return
	}

	runTUI(ip, label, opts)
}

func runTUI(ip net.IP, label string, opts options) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := ui.Channels{
		Ping:  make(chan probe.PingStats, 8),
		Trace: make(chan probe.TraceSnapshot, 8),
		Speed: make(chan probe.SpeedProgress, 16),
	}

	go probe.NewPinger(ip, label, opts.interval, opts.timeout).Run(ctx, ch.Ping)
	go probe.NewTracer(ip, label, opts.maxHops, opts.interval, opts.timeout).Run(ctx, ch.Trace)

	prog := tea.NewProgram(ui.New(ctx, label, ip.String(), ch, time.Now()), tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка UI:", err)
		os.Exit(1)
	}
}

// runHeadless collects ping and trace samples for opts.duration, then (unless
// -no-speed) runs the throughput test, then prints a one-shot report. Progress
// messages go to stderr so stdout stays clean for piping JSON.
func runHeadless(ip net.IP, label string, opts options) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pingCh := make(chan probe.PingStats, 8)
	traceCh := make(chan probe.TraceSnapshot, 8)
	go probe.NewPinger(ip, label, opts.interval, opts.timeout).Run(ctx, pingCh)
	go probe.NewTracer(ip, label, opts.maxHops, opts.interval, opts.timeout).Run(ctx, traceCh)

	var (
		mu        sync.Mutex
		lastPing  probe.PingStats
		lastTrace probe.TraceSnapshot
	)

	collectCtx, stopCollect := context.WithCancel(ctx)
	go func() {
		for {
			select {
			case <-collectCtx.Done():
				return
			case p := <-pingCh:
				mu.Lock()
				lastPing = p
				mu.Unlock()
			case s := <-traceCh:
				mu.Lock()
				lastTrace = s
				mu.Unlock()
			}
		}
	}()

	progress := !opts.jsonOut
	start := time.Now()
	if progress {
		fmt.Fprintf(os.Stderr, "Сбор данных %s до %s…\n", opts.duration, label)
	}
	select {
	case <-ctx.Done():
		stopCollect()
		return ctx.Err()
	case <-time.After(opts.duration):
	}
	stopCollect()

	var lastSpeed probe.SpeedProgress
	if !opts.noSpeed {
		if progress {
			fmt.Fprintln(os.Stderr, "Тест скорости (~20с)…")
		}
		lastSpeed = runSpeed(ctx)
	}

	cancel()

	mu.Lock()
	pingFinal, traceFinal := lastPing, lastTrace
	mu.Unlock()

	r := report.Build(report.Options{
		Target:      label,
		IP:          ip.String(),
		Version:     version,
		GeneratedAt: time.Now(),
		Duration:    time.Since(start).Round(time.Second),
	}, &pingFinal, &traceFinal, &lastSpeed)

	if opts.jsonOut {
		return report.WriteJSON(os.Stdout, r)
	}
	return report.WriteText(os.Stdout, r)
}

// runSpeed runs a full speed test and returns the terminal snapshot. The
// speed channel is never closed by RunSpeedTest, so we stop reading at
// PhaseDone / PhaseError instead of ranging until close.
func runSpeed(parent context.Context) probe.SpeedProgress {
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	ch := make(chan probe.SpeedProgress, 16)
	go probe.RunSpeedTest(ctx, ch)
	var last probe.SpeedProgress
	for {
		select {
		case <-ctx.Done():
			return last
		case s := <-ch:
			last = s
			if s.Phase == probe.PhaseDone || s.Phase == probe.PhaseError {
				return last
			}
		}
	}
}
