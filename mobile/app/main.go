// Command app is the Fyne GUI front-end for net-test: a normal Go program that
// imports internal/probe directly and renders a touch UI. `fyne package`
// compiles it straight to an Android APK; it also runs as a desktop window for
// quick iteration.
//
// Build:
//
//	go run gen.go                      # (re)generate Icon.png
//	go build .                         # desktop compile-check
//	make -C ../.. apk                  # Android APK via fyne package
//
//go:generate go run gen.go
package main

import (
	"context"
	"fmt"
	"image/color"
	"net"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/tavvet/net-test/internal/probe"
)

// view holds the widgets the controller updates plus the root object to show.
// Separating construction (newView) from behaviour (wire) lets the headless
// renderer in render_test.go build the exact same UI tree without a display.
type view struct {
	root        fyne.CanvasObject
	target      *widget.Entry
	status      *widget.Label
	tabs        *container.AppTabs
	startBtn    *widget.Button
	speedBtn    *widget.Button
	ping        *widget.Label
	diag        *widget.Label
	speed       *widget.Label
	routeBanner *canvas.Text    // headline above the route: where loss starts
	hopBox      *fyne.Container // VBox of per-hop rows, rebuilt on each snapshot
}

func newView() *view {
	v := &view{}
	v.target = widget.NewEntry()
	v.target.SetText("1.1.1.1")
	v.status = widget.NewLabel("Готов")
	v.ping = monoLabel("Нажмите «Старт».")
	v.diag = monoLabel("—")
	v.speed = monoLabel("Нажмите «Запустить тест скорости».")

	v.routeBanner = canvas.NewText("Нажмите «Старт».", colDim)
	v.routeBanner.TextStyle = fyne.TextStyle{Bold: true}
	v.hopBox = container.NewVBox()

	v.speedBtn = widget.NewButton("Запустить тест скорости", nil)
	v.startBtn = widget.NewButton("Старт", nil)

	v.tabs = container.NewAppTabs(
		container.NewTabItem("Пинг", container.NewVScroll(v.ping)),
		container.NewTabItem("Маршрут", container.NewBorder(
			container.NewPadded(v.routeBanner), nil, nil, nil,
			container.NewVScroll(v.hopBox),
		)),
		container.NewTabItem("Диагноз", container.NewVScroll(v.diag)),
		container.NewTabItem("Скорость", container.NewBorder(v.speedBtn, nil, nil, nil, container.NewVScroll(v.speed))),
	)

	top := container.NewBorder(nil, nil, nil, v.startBtn, v.target)
	v.root = container.NewBorder(top, v.status, nil, nil, v.tabs)
	return v
}

func main() {
	a := app.New()
	w := a.NewWindow("net-test")

	// One app-wide context; closing the window cancels every in-flight probe.
	// On Android the Go process can outlive the Activity, so without this the
	// pinger/tracer would keep sending ICMP in the background.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	v := newView()
	wire(ctx, v)
	w.SetOnClosed(cancel)
	w.SetContent(v.root)
	w.Resize(fyne.NewSize(420, 760))
	w.ShowAndRun()
}

// wire attaches behaviour to a view. Старт/Стоп drives a ping+trace run — a
// child of appCtx, so closing the window cancels it too — whose snapshots stream
// into the tabs; the speed button runs one throughput test. DNS resolution runs
// off the UI goroutine so a slow hostname can't freeze the UI. Every
// cross-goroutine widget update goes through fyne.Do (Fyne v2.7 requires
// main-thread updates) and is tagged with a per-run epoch, so a snapshot queued
// just before Стоп or a restart can't repaint a newer run's tabs.
func wire(appCtx context.Context, v *view) {
	var (
		cancel context.CancelFunc // cancels the active run; nil when idle
		epoch  int                // bumped on every Старт/Стоп; guards stale fyne.Do closures
	)
	stop := func() {
		if cancel != nil {
			cancel()
			cancel = nil
		}
		epoch++
	}

	v.startBtn.OnTapped = func() {
		if cancel != nil { // running → stop
			stop()
			v.startBtn.SetText("Старт")
			v.status.SetText("Остановлено")
			return
		}
		target := strings.TrimSpace(v.target.Text)
		epoch++
		gen := epoch
		ctx, c := context.WithCancel(appCtx)
		cancel = c
		v.startBtn.SetText("Стоп")
		v.status.SetText("Разрешаю " + target + "…")

		go func() {
			ip, label, err := probe.Resolve(target) // may block on DNS — off the UI thread
			if err != nil {
				fyne.Do(func() {
					if gen != epoch { // superseded by Стоп or a restart
						return
					}
					stop()
					v.startBtn.SetText("Старт")
					v.status.SetText("Ошибка: " + err.Error())
				})
				return
			}
			if ctx.Err() != nil { // stopped while resolving
				return
			}
			fyne.Do(func() {
				if gen == epoch {
					v.status.SetText(fmt.Sprintf("Проверка %s (%s)…", label, ip))
				}
			})

			pingCh := make(chan probe.PingStats, 8)
			traceCh := make(chan probe.TraceSnapshot, 8)
			go probe.NewPinger(ip, label, time.Second, 2*time.Second).Run(ctx, pingCh)
			go probe.NewTracer(ip, label, 30, time.Second, 2*time.Second).Run(ctx, traceCh)

			for {
				select {
				case <-ctx.Done():
					return
				case p := <-pingCh:
					fyne.Do(func() {
						if gen == epoch {
							v.ping.SetText(pingText(p))
						}
					})
				case s := <-traceCh:
					fyne.Do(func() {
						if gen != epoch {
							return
						}
						v.setRoute(s)
						v.diag.SetText(diagText(s))
					})
				}
			}
		}()
	}

	v.speedBtn.OnTapped = func() {
		v.speedBtn.Disable()
		v.speed.SetText("Тест скорости (~20с)…")
		go func() {
			ctx, c := context.WithTimeout(appCtx, 60*time.Second)
			defer c()
			ch := make(chan probe.SpeedProgress, 16)
			go probe.RunSpeedTest(ctx, ch)
			for {
				select {
				case <-ctx.Done():
					fyne.Do(v.speedBtn.Enable)
					return
				case sp := <-ch:
					fyne.Do(func() { v.speed.SetText(speedText(sp)) })
					if sp.Phase == probe.PhaseDone || sp.Phase == probe.PhaseError {
						fyne.Do(v.speedBtn.Enable)
						return
					}
				}
			}
		}()
	}
}

func monoLabel(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.TextStyle = fyne.TextStyle{Monospace: true}
	l.Wrapping = fyne.TextWrapWord
	return l
}

func pingText(p probe.PingStats) string {
	if p.Err != "" {
		return "Ошибка: " + p.Err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "RTT текущий : %.1f ms\n", probe.Millis(p.LastRTT))
	fmt.Fprintf(&b, "сред/мин/макс: %.1f / %.1f / %.1f ms\n",
		probe.Millis(p.AvgRTT), probe.Millis(p.BestRTT), probe.Millis(p.WorstRTT))
	fmt.Fprintf(&b, "Потери       : %.1f%%  (%d/%d)\n", p.LossPct, p.Recv, p.Sent)
	fmt.Fprintf(&b, "Джиттер      : %.1f ms\n\n", probe.Millis(p.Jitter))
	fmt.Fprintf(&b, "Качество     : %s", verdict(p))
	return b.String()
}

// verdict mirrors the TUI's quality verdict for the mobile UI: it shares
// probe.Quality (the loss/jitter thresholds) and probe.MinVerdictSamples, so the
// two front-ends never disagree on the same data.
func verdict(p probe.PingStats) string {
	if p.WindowSize < probe.MinVerdictSamples {
		return "сбор данных…"
	}
	level, lossDominates := probe.Quality(p.WindowLossPct, probe.Millis(p.WindowJitter))
	label := qualityLabel(level)
	switch {
	case level == probe.QualityPerfect:
		return label
	case lossDominates:
		return fmt.Sprintf("%s (потери %.1f%%)", label, p.WindowLossPct)
	default:
		return fmt.Sprintf("%s (джиттер %.0f ms)", label, probe.Millis(p.WindowJitter))
	}
}

func qualityLabel(q probe.QualityLevel) string {
	switch q {
	case probe.QualityCritical:
		return "Критично"
	case probe.QualityBad:
		return "Плохо"
	case probe.QualityGood:
		return "Хорошо"
	default:
		return "Отлично"
	}
}

// ---- route (loss map) ----

// Severity palette for the loss gauge — fixed colours (not theme-derived) so the
// four quality levels stay visually distinct; tuned to read on the default dark
// theme. Neutral text uses the theme's foreground/disabled colours instead.
var (
	colDim  = color.NRGBA{0x8a, 0x8a, 0x8a, 0xff}
	colGood = color.NRGBA{0x3d, 0xc0, 0x6c, 0xff}
	colOK   = color.NRGBA{0xd9, 0x9e, 0x1f, 0xff}
	colWarn = color.NRGBA{0xe2, 0x7d, 0x2f, 0xff}
	colBad  = color.NRGBA{0xdb, 0x4b, 0x4b, 0xff}
)

// qualityColor maps a probe quality level to its gauge colour. probe.Quality is
// the shared source of the thresholds; this just mirrors the TUI's palette intent.
func qualityColor(q probe.QualityLevel) color.NRGBA {
	switch q {
	case probe.QualityCritical:
		return colBad
	case probe.QualityBad:
		return colWarn
	case probe.QualityGood:
		return colOK
	default:
		return colGood
	}
}

// routeHeadline mirrors the TUI banner: where loss (or a latency jump) first
// appears on the route, or an all-clear. The wording and the culprit hop come
// from probe (FirstIssueHop/FirstIssueLoss) so the two front-ends never disagree.
func routeHeadline(d probe.Diagnosis) (string, color.NRGBA) {
	switch {
	case len(d.Segments) == 0:
		return "Сбор маршрута…", colDim
	case d.Healthy:
		return "✓ Потерь по маршруту нет", colGood
	case d.FirstIssueLoss:
		return fmt.Sprintf("⚠ Потери начинаются на хопе %d · %s", d.FirstIssueHop, d.FirstIssue), colBad
	default:
		return fmt.Sprintf("↑ Рост задержки на хопе %d · %s", d.FirstIssueHop, d.FirstIssue), colWarn
	}
}

// setRoute repaints the Маршрут tab from a trace snapshot: the headline banner
// plus one rich row per hop. Hops are few (≤ max-hops), so rebuilding the rows
// each snapshot is cheaper and simpler than diffing a recycling list.
func (v *view) setRoute(s probe.TraceSnapshot) {
	if s.Err != "" {
		v.routeBanner.Text, v.routeBanner.Color = "Ошибка: "+s.Err, colBad
		v.routeBanner.Refresh()
		v.hopBox.Objects = nil
		v.hopBox.Refresh()
		return
	}

	text, col := routeHeadline(s.Diagnosis)
	v.routeBanner.Text, v.routeBanner.Color = text, col
	v.routeBanner.Refresh()

	// Scale the gauge to the worst hop on the route, floored at 10% so a
	// near-clean route doesn't render alarming near-full bars.
	lossScale := 10.0
	for _, h := range s.Hops {
		if h.LossPct > lossScale {
			lossScale = h.LossPct
		}
	}
	rows := make([]fyne.CanvasObject, len(s.Hops))
	for i, h := range s.Hops {
		rows[i] = newHopRow(h, lossScale)
	}
	v.hopBox.Objects = rows
	v.hopBox.Refresh()
}

// newHopRow builds one route row: a flag+TTL gutter, host over zone, the loss
// gauge underneath, and loss%/RTT on the right. Persistent-anomaly hops get the
// ⚠ marker and a severity-coloured zone so the problem node stands out.
func newHopRow(h probe.Hop, lossScale float64) fyne.CanvasObject {
	fg := theme.Color(theme.ColorNameForeground)
	dim := theme.Color(theme.ColorNameDisabled)

	level, _ := probe.Quality(h.LossPct, 0)
	sev := qualityColor(level)
	if level == probe.QualityPerfect {
		sev = colDim // 0% loss → neutral, no alarm
	}
	flagged := h.LossPersists || h.RTTPersists

	ttlStr, ttlColor := fmt.Sprintf("  %2d", h.TTL), dim
	if flagged {
		ttlStr, ttlColor = fmt.Sprintf("⚠ %2d", h.TTL), color.Color(colBad)
	}
	zoneColor := dim
	if flagged {
		zoneColor = sev
	}
	lossColor := dim
	if h.LossPct > 0 {
		lossColor = sev
	}

	loss := monoText(fmt.Sprintf("%.0f%%", h.LossPct), lossColor)
	loss.Alignment = fyne.TextAlignTrailing
	rtt := monoText(hopRTT(h), dim)
	rtt.Alignment = fyne.TextAlignTrailing

	top := container.NewBorder(
		nil, nil,
		monoText(ttlStr, ttlColor),
		container.NewVBox(loss, rtt),
		container.NewVBox(monoText(truncRunes(hopName(h), 30), fg), monoText(truncRunes(hopZoneLabel(h), 30), zoneColor)),
	)
	return container.NewVBox(top, monoText(lossBar(h.LossPct, lossScale), sev))
}

// truncRunes caps a string to max runes (canvas.Text doesn't wrap), so a long
// reverse-DNS name can't overflow into the loss column on a narrow screen.
func truncRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

// lossBar renders packet loss as a 14-cell gauge: filled blocks proportional to
// loss (scaled to the worst hop on the route), the rest a faint track.
func lossBar(lossPct, scale float64) string {
	const width = 14
	n := 0
	if scale > 0 {
		n = int(lossPct/scale*float64(width) + 0.5)
	}
	if n > width {
		n = width
	}
	if n < 0 {
		n = 0
	}
	return strings.Repeat("█", n) + strings.Repeat("░", width-n)
}

func hopName(h probe.Hop) string {
	switch {
	case h.Host != "":
		return h.Host
	case h.IP != "":
		return h.IP
	default:
		return "*"
	}
}

// hopZoneLabel mirrors the TUI's networkLabel: local IP → "локальная сеть", else
// the shortened AS name, else "" while the lookup is still in flight.
func hopZoneLabel(h probe.Hop) string {
	if ip := net.ParseIP(h.IP); ip != nil && probe.IsLocalIP(ip) {
		return "локальная сеть"
	}
	if h.ASName != "" {
		return probe.ShortenASName(h.ASName)
	}
	return ""
}

func hopRTT(h probe.Hop) string {
	if h.Recv == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f ms", probe.Millis(h.AvgRTT))
}

func monoText(s string, c color.Color) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextStyle = fyne.TextStyle{Monospace: true}
	return t
}

func diagText(s probe.TraceSnapshot) string {
	d := s.Diagnosis
	if len(d.Segments) == 0 {
		return "Сбор маршрута…"
	}
	var b strings.Builder
	if d.Healthy {
		b.WriteString("Состояние: маршрут здоров\n\n")
	} else {
		fmt.Fprintf(&b, "Состояние: проблема в зоне «%s»\n\n", d.FirstIssue)
	}
	for _, seg := range d.Segments {
		mark := "✓"
		switch {
		case !seg.Healthy:
			mark = "⚠"
		case seg.Kind == probe.SegmentUnknown:
			mark = "·"
		}
		span := fmt.Sprintf("хоп %d", seg.HopFrom)
		if seg.HopTo != seg.HopFrom {
			span = fmt.Sprintf("хопы %d-%d", seg.HopFrom, seg.HopTo)
		}
		fmt.Fprintf(&b, "%s %s  (%s)\n", mark, seg.Label, span)
		if !seg.Healthy && seg.Issue != "" {
			fmt.Fprintf(&b, "    → %s\n", seg.Issue)
		}
	}
	return b.String()
}

func speedText(sp probe.SpeedProgress) string {
	if sp.Err != "" {
		return "Ошибка: " + sp.Err
	}
	var b strings.Builder
	if sp.Server != "" {
		fmt.Fprintf(&b, "Сервер CF : %s\n", sp.Server)
	}
	if sp.IP != "" {
		fmt.Fprintf(&b, "Ваш IP    : %s\n", sp.IP)
	}
	fmt.Fprintf(&b, "Латентность: %.0f ms  (джиттер %.0f)\n", sp.LatencyMs, sp.JitterMs)
	fmt.Fprintf(&b, "Загрузка ↓: %.1f Mbps\n", sp.DownloadMbps)
	fmt.Fprintf(&b, "Отдача   ↑: %.1f Mbps\n", sp.UploadMbps)
	if sp.Phase != probe.PhaseDone && sp.Phase != probe.PhaseError {
		fmt.Fprintf(&b, "\n%s… %.0f%%  (%.1f Mbps)", sp.Phase, sp.Percent*100, sp.Mbps)
	}
	return b.String()
}
