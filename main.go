// net-test is a console-GUI utility for checking connection quality: live
// latency/loss/jitter to a target, an mtr-style per-hop route view that shows
// WHERE drops happen, and a Cloudflare-based download/upload speed test.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/tavvet/net-test/internal/probe"
	"github.com/tavvet/net-test/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// version is overridden at build time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	target := flag.String("target", "1.1.1.1", "хост или IP для проверки соединения")
	interval := flag.Duration("interval", time.Second, "интервал между пробами (пинг и трассировка)")
	timeout := flag.Duration("timeout", 2*time.Second, "таймаут ожидания ICMP-ответа")
	maxHops := flag.Int("max-hops", 30, "максимум хопов для трассировки маршрута")
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	flag.Parse()

	if *showVersion {
		fmt.Println("net-test", version)
		return
	}

	ip, label, err := probe.Resolve(*target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := ui.Channels{
		Ping:  make(chan probe.PingStats, 8),
		Trace: make(chan probe.TraceSnapshot, 8),
		Speed: make(chan probe.SpeedProgress, 16),
	}

	go probe.NewPinger(ip, label, *interval, *timeout).Run(ctx, ch.Ping)
	go probe.NewTracer(ip, label, *maxHops, *interval, *timeout).Run(ctx, ch.Trace)

	prog := tea.NewProgram(ui.New(ctx, label, ip.String(), ch, time.Now()), tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка UI:", err)
		os.Exit(1)
	}
}
