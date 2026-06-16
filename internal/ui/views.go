package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/tavvet/net-test/internal/probe"

	"github.com/charmbracelet/lipgloss"
)

func (m model) render() string {
	header := m.header()
	tabs := m.tabBar()
	footer := m.footer()

	var body string
	switch m.tab {
	case tabPing:
		body = m.viewPing()
	case tabTrace:
		body = m.viewTrace()
	case tabSpeed:
		body = m.viewSpeed()
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, tabs, "", body, "", footer)
}

func (m model) header() string {
	left := titleStyle.Render("net-test")
	parts := []string{
		labelStyle.Render(" цель ") + bold.Render(m.target),
	}
	if m.speed.Server != "" {
		parts = append(parts, labelStyle.Render("CF ")+m.speed.Server)
	}
	parts = append(parts, labelStyle.Render("время ")+time.Since(m.started).Round(time.Second).String())
	info := strings.Join(parts, labelStyle.Render("  │  "))
	line := left + "  " + info
	return lipgloss.NewStyle().Width(m.w).Render(line)
}

func (m model) tabBar() string {
	var cells []string
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if tab(i) == m.tab {
			cells = append(cells, tabActive.Render(label))
		} else {
			cells = append(cells, tabInactive.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cells...)
}

func (m model) footer() string {
	k := func(key, desc string) string { return keyStyle.Render(key) + footerStyle.Render(" "+desc) }
	hints := []string{
		k("1-3/⭾", "вкладки"),
		k("s", "тест скорости"),
		k("q", "выход"),
	}
	return footerStyle.Render(strings.Join(hints, footerStyle.Render("   ")))
}

// ---- Ping tab ----

func (m model) viewPing() string {
	if !m.havePing {
		return m.spin.View() + " измеряю задержку…"
	}
	p := m.ping
	if p.Err != "" {
		return lipgloss.NewStyle().Foreground(cBad).Render("Ошибка: " + p.Err)
	}

	cards := lipgloss.JoinHorizontal(lipgloss.Top,
		stat("Текущий RTT", fmtRTT(p.LastRTT), colorRTT(ms(p.LastRTT))),
		stat("Средний", fmtRTT(p.AvgRTT), colorRTT(ms(p.AvgRTT))),
		stat("Джиттер", fmtRTT(p.Jitter), colorRTT(ms(p.Jitter)*4)),
		stat("Потери", fmt.Sprintf("%.1f%%", p.LossPct), colorLoss(p.LossPct)),
	)

	label, reason, vc := verdict(p.LossPct, ms(p.Jitter))
	qText := lipgloss.NewStyle().Bold(true).Foreground(vc).Render(label)
	if reason != "" {
		qText += labelStyle.Render(" (" + reason + ")")
	}
	verdictLine := labelStyle.Render("Качество: ") + qText +
		labelStyle.Render(fmt.Sprintf("   отправлено %d · получено %d · min %s · max %s",
			p.Sent, p.Recv, fmtRTT(p.BestRTT), fmtRTT(p.WorstRTT)))

	sparkW := m.w - 6 // content area minus border+padding
	spark := boxStyle.Width(m.w - 4).Render(
		labelStyle.Render(fmt.Sprintf("История RTT (последние %d проб, ✕ = потеря)", len(p.History))) + "\n" +
			sparkline(p.History, sparkW))

	return lipgloss.JoinVertical(lipgloss.Left, cards, "", verdictLine, "", spark)
}

// ---- Route (mtr) tab ----

var traceCols = []struct {
	name  string
	w     int
	right bool
}{
	{"#", 3, true},
	{"Хост / IP", 0, false}, // flex
	{"Потери", 8, true},
	{"Отпр", 5, true},
	{"Послед", 9, true},
	{"Сред", 9, true},
	{"Лучш", 9, true},
	{"Худш", 9, true},
	{"СКО", 8, true},
}

func (m model) viewTrace() string {
	if !m.haveTrace {
		return m.spin.View() + " трассирую маршрут…"
	}
	t := m.trace
	if t.Err != "" {
		return lipgloss.NewStyle().Foreground(cBad).Render("Ошибка: " + t.Err)
	}

	fixed := 0
	for _, c := range traceCols {
		fixed += c.w
	}
	gaps := len(traceCols) // one space between/after columns
	hostW := max(m.w-fixed-gaps, 10)

	colW := func(i int) int {
		if traceCols[i].w == 0 {
			return hostW
		}
		return traceCols[i].w
	}

	// header
	var head []string
	for i, c := range traceCols {
		head = append(head, cell(c.name, colW(i), c.right, headStyle))
	}
	rows := []string{strings.Join(head, " ")}

	// limit rows to available vertical space
	maxRows := m.h - 8
	hops := t.Hops
	if maxRows > 0 && len(hops) > maxRows {
		hops = hops[len(hops)-maxRows:]
	}

	for _, h := range hops {
		host := "*"
		hc := lipgloss.NewStyle().Foreground(cMuted)
		if h.IP != "" {
			host = h.IP
			hc = lipgloss.NewStyle()
			if h.Host != "" {
				host = fmt.Sprintf("%s (%s)", h.Host, h.IP)
			}
		}
		host = truncate(host, hostW)
		lossStr := fmt.Sprintf("%.0f%%", h.LossPct)

		cells := []string{
			cell(fmt.Sprintf("%d", h.TTL), colW(0), true, lipgloss.NewStyle().Foreground(cDim)),
			cell(host, colW(1), false, hc),
			cell(lossStr, colW(2), true, lipgloss.NewStyle().Foreground(colorLoss(h.LossPct))),
			cell(fmt.Sprintf("%d", h.Sent), colW(3), true, lipgloss.NewStyle().Foreground(cDim)),
			cell(rttCell(h.LastRTT), colW(4), true, lipgloss.NewStyle().Foreground(colorRTT(ms(h.LastRTT)))),
			cell(rttCell(h.AvgRTT), colW(5), true, lipgloss.NewStyle().Foreground(colorRTT(ms(h.AvgRTT)))),
			cell(rttCell(h.BestRTT), colW(6), true, lipgloss.NewStyle().Foreground(cDim)),
			cell(rttCell(h.WorstRTT), colW(7), true, lipgloss.NewStyle().Foreground(cDim)),
			cell(rttCell(h.StdDev), colW(8), true, lipgloss.NewStyle().Foreground(cDim)),
		}
		rows = append(rows, strings.Join(cells, " "))
	}
	return strings.Join(rows, "\n")
}

// ---- Speed tab ----

func (m model) viewSpeed() string {
	s := m.speed
	var lines []string

	meta := labelStyle.Render("Сервер Cloudflare: ")
	if s.Server != "" {
		meta += bold.Render(s.Server)
	} else {
		meta += labelStyle.Render("—")
	}
	if s.IP != "" {
		meta += labelStyle.Render("    ваш IP: ") + s.IP
	}
	lines = append(lines, meta, "")

	// status line
	switch {
	case s.Phase == probe.PhaseError:
		lines = append(lines, lipgloss.NewStyle().Foreground(cBad).Render("Ошибка: "+s.Err))
	case m.speedRun:
		lines = append(lines, m.spin.View()+" "+bold.Render(s.Phase.String())+"…")
	case s.Phase == probe.PhaseDone:
		lines = append(lines, lipgloss.NewStyle().Foreground(cGood).Render("✓ Тест завершён"))
	default:
		lines = append(lines, labelStyle.Render("Нажмите ")+keyStyle.Render("s")+labelStyle.Render(" чтобы запустить тест скорости"))
	}
	lines = append(lines, "")

	// scale for throughput bars
	scaleMax := 50.0
	for _, v := range []float64{s.DownloadMbps, s.UploadMbps, s.Mbps} {
		if v*1.1 > scaleMax {
			scaleMax = v * 1.1
		}
	}
	barW := max(m.w/3, 10)

	// latency
	latColor := colorRTT(s.LatencyMs)
	latVal := "—"
	if s.LatencyMs > 0 {
		latVal = fmt.Sprintf("%.1f ms", s.LatencyMs)
	}
	latLine := speedLabel("Латентность") +
		lipgloss.NewStyle().Bold(true).Foreground(latColor).Render(latVal) +
		labelStyle.Render(fmt.Sprintf("   джиттер %.1f ms", s.JitterMs))
	lines = append(lines, latLine)

	// download / upload
	lines = append(lines,
		speedRow("Загрузка ↓", s.DownloadMbps, liveVal(s, probe.PhaseDownload), scaleMax, barW, cGood),
		speedRow("Отдача ↑", s.UploadMbps, liveVal(s, probe.PhaseUpload), scaleMax, barW, cAccent),
	)
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// liveVal returns the in-progress Mbps when `phase` is the one currently running.
func liveVal(s probe.SpeedProgress, phase probe.SpeedPhase) float64 {
	if s.Phase == phase {
		return s.Mbps
	}
	return 0
}

func speedRow(label string, final, live, scaleMax float64, barW int, color lipgloss.Color) string {
	val := final
	if final == 0 && live > 0 {
		val = live
	}
	gauge := bar(val/scaleMax, barW, color)
	num := lipgloss.NewStyle().Bold(true).Foreground(color).Render(fmtMbps(val))
	return speedLabel(label) + gauge + "  " + num
}

func speedLabel(s string) string {
	return lipgloss.NewStyle().Width(16).Foreground(cDim).Render(s)
}

// ---- small helpers ----

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func rttCell(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f", ms(d))
}

func cell(s string, w int, right bool, st lipgloss.Style) string {
	a := lipgloss.Left
	if right {
		a = lipgloss.Right
	}
	return st.Width(w).Align(a).Render(s)
}

func truncate(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	r := []rune(s)
	if len(r) > w-1 {
		r = r[:w-1]
	}
	return string(r) + "…"
}
