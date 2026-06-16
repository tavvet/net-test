package ui

import (
	"fmt"
	"net"
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
	case tabDiagnosis:
		body = m.viewDiagnosis()
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
		k("1-4/⭾", "вкладки"),
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
	{"", 2, false}, // anomaly flag: "⚠" or blank
	{"#", 3, true},
	{"Хост / IP", 0, false}, // flex
	{"Потери", 8, true},
	{"Отпр", 5, true},
	{"Послед", 9, true},
	{"Сред", 11, true}, // wider: also shows "+ΔX" suffix on flagged hops
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
			if suffix := networkLabel(h); suffix != "" {
				host = fmt.Sprintf("%s · %s", host, suffix)
			}
		}
		host = truncate(host, hostW)

		flag, lossStyle, avgStr, avgStyle := anomalyDecor(h)
		dimStyle := lipgloss.NewStyle().Foreground(cDim)
		cells := []string{
			cell(flag, colW(0), false, lipgloss.NewStyle().Foreground(cBad).Bold(true)),
			cell(fmt.Sprintf("%d", h.TTL), colW(1), true, dimStyle),
			cell(host, colW(2), false, hc),
			cell(fmt.Sprintf("%.0f%%", h.LossPct), colW(3), true, lossStyle),
			cell(fmt.Sprintf("%d", h.Sent), colW(4), true, dimStyle),
			cell(rttCell(h.LastRTT), colW(5), true, lipgloss.NewStyle().Foreground(colorRTT(ms(h.LastRTT)))),
			cell(avgStr, colW(6), true, avgStyle),
			cell(rttCell(h.BestRTT), colW(7), true, dimStyle),
			cell(rttCell(h.WorstRTT), colW(8), true, dimStyle),
			cell(rttCell(h.StdDev), colW(9), true, dimStyle),
		}
		rows = append(rows, strings.Join(cells, " "))
	}
	return strings.Join(rows, "\n")
}

// networkLabel returns a short label for the network the hop belongs to: an
// AS name for public IPs (when the Team Cymru lookup has returned), or
// "локальная сеть" for private/loopback/link-local/CGNAT. Empty while the
// lookup is still in flight, so the row simply doesn't show a suffix yet.
func networkLabel(h probe.Hop) string {
	if ip := net.ParseIP(h.IP); ip != nil && isLocalIP(ip) {
		return "локальная сеть"
	}
	if h.ASName != "" {
		return shortenASName(h.ASName)
	}
	return ""
}

// shortenASName trims Team Cymru's verbose form to the leading token.
// Example: "CLOUDFLARENET - Cloudflare, Inc., US" → "CLOUDFLARENET".
func shortenASName(name string) string {
	if i := strings.Index(name, " - "); i > 0 {
		return name[:i]
	}
	if i := strings.Index(name, ","); i > 0 {
		return name[:i]
	}
	return name
}

// isLocalIP duplicates the predicate from internal/probe so the UI can format
// private hops without importing the probe-internal helper.
func isLocalIP(ip net.IP) bool {
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}

// anomalyDecor turns a hop's *Persists/DeltaRTT flags into render decorations
// for the trace row: a flag glyph, the styled loss cell, and the avg-RTT cell
// (which may carry a "+ΔX" suffix on hops with persistent RTT rise).
func anomalyDecor(h probe.Hop) (flag string, lossStyle lipgloss.Style, avgStr string, avgStyle lipgloss.Style) {
	if h.LossPersists || h.RTTPersists {
		flag = "⚠"
	}

	lossStyle = lipgloss.NewStyle().Foreground(colorLoss(h.LossPct))
	if h.LossPersists {
		lossStyle = lossStyle.Bold(true)
	}

	avgStr = rttCell(h.AvgRTT)
	avgStyle = lipgloss.NewStyle().Foreground(colorRTT(ms(h.AvgRTT)))
	if h.RTTPersists && h.DeltaRTT > 0 {
		avgStr = fmt.Sprintf("%s +%.0f", avgStr, ms(h.DeltaRTT))
		avgStyle = avgStyle.Bold(true)
	}
	return flag, lossStyle, avgStr, avgStyle
}

// ---- Diagnosis tab ----

func (m model) viewDiagnosis() string {
	if !m.haveTrace {
		return m.spin.View() + " собираю маршрут для диагноза…"
	}
	t := m.trace
	if t.Err != "" {
		return lipgloss.NewStyle().Foreground(cBad).Render("Ошибка: " + t.Err)
	}
	if len(t.Diagnosis.Segments) == 0 {
		return labelStyle.Render("Маршрут ещё пуст.")
	}

	heading := labelStyle.Render("Маршрут до ") + bold.Render(t.Target)
	overall := overallVerdict(t.Diagnosis)
	lines := []string{heading, overall, ""}

	rangeW := 9
	labelW := 28

	for _, seg := range t.Diagnosis.Segments {
		mark, color := segmentMark(seg)
		markCell := lipgloss.NewStyle().Foreground(color).Bold(true).Width(2).Render(mark)
		labelCell := lipgloss.NewStyle().Width(labelW).Render(truncate(seg.Label, labelW))
		rangeCell := lipgloss.NewStyle().Width(rangeW).Foreground(cDim).Render(hopRange(seg))
		lines = append(lines, markCell+" "+labelCell+rangeCell)
		if !seg.Healthy && seg.Issue != "" {
			indent := strings.Repeat(" ", 3)
			lines = append(lines, indent+lipgloss.NewStyle().Foreground(color).Render("→ "+seg.Issue))
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// segmentMark picks the indicator glyph and color for a segment based on its
// health. Healthy segments get ✓ green; problematic ones ⚠ red.
func segmentMark(s probe.Segment) (string, lipgloss.Color) {
	if !s.Healthy {
		return "⚠", cBad
	}
	if s.Kind == probe.SegmentUnknown {
		return "·", cDim
	}
	return "✓", cGood
}

func hopRange(s probe.Segment) string {
	if s.HopFrom == s.HopTo {
		return fmt.Sprintf("хоп %d", s.HopFrom)
	}
	return fmt.Sprintf("хопы %d-%d", s.HopFrom, s.HopTo)
}

// overallVerdict summarises the segments into one line: which segment first
// went bad, or that everything is fine.
func overallVerdict(d probe.Diagnosis) string {
	for _, s := range d.Segments {
		if !s.Healthy {
			return labelStyle.Render("Состояние: ") +
				lipgloss.NewStyle().Bold(true).Foreground(cBad).Render("проблема в зоне «"+s.Label+"»")
		}
	}
	return labelStyle.Render("Состояние: ") +
		lipgloss.NewStyle().Bold(true).Foreground(cGood).Render("маршрут здоров")
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
