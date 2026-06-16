package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/tavvet/net-test/internal/probe"

	"github.com/charmbracelet/lipgloss"
)

// Palette.
var (
	cGood   = lipgloss.Color("42")  // green
	cOK     = lipgloss.Color("220") // yellow
	cWarn   = lipgloss.Color("208") // orange
	cBad    = lipgloss.Color("203") // red
	cAccent = lipgloss.Color("39")  // cyan/blue
	cDim    = lipgloss.Color("245")
	cMuted  = lipgloss.Color("240")
	cBright = lipgloss.Color("231")
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(cBright).Background(cAccent).Padding(0, 1)
	tabActive    = lipgloss.NewStyle().Bold(true).Foreground(cBright).Background(cAccent).Padding(0, 2)
	tabInactive  = lipgloss.NewStyle().Foreground(cDim).Padding(0, 2)
	footerStyle  = lipgloss.NewStyle().Foreground(cMuted)
	keyStyle     = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	boxStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cMuted).Padding(0, 1)
	labelStyle   = lipgloss.NewStyle().Foreground(cDim)
	headStyle    = lipgloss.NewStyle().Foreground(cDim).Bold(true)
	bold         = lipgloss.NewStyle().Bold(true)
	spinnerStyle = lipgloss.NewStyle().Foreground(cAccent)
)

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// colorRTT maps a latency in ms to a quality color.
func colorRTT(ms float64) lipgloss.Color {
	switch {
	case ms <= 0:
		return cMuted
	case ms < 50:
		return cGood
	case ms < 120:
		return cOK
	default:
		return cBad
	}
}

// colorLoss maps a packet-loss percentage to a quality color.
func colorLoss(pct float64) lipgloss.Color {
	switch {
	case pct <= 0:
		return cGood
	case pct < 5:
		return cOK
	default:
		return cBad
	}
}

// sparkline renders the last `width` RTT samples as colored block characters.
// A zero sample (lost probe) renders as a red ✕.
func sparkline(vals []float64, width int) string {
	if width < 1 {
		width = 1
	}
	if len(vals) > width {
		vals = vals[len(vals)-width:]
	}
	// scale over non-zero samples
	min, max := 0.0, 0.0
	first := true
	for _, v := range vals {
		if v <= 0 {
			continue
		}
		if first || v < min {
			min = v
		}
		if first || v > max {
			max = v
		}
		first = false
	}
	var b strings.Builder
	for _, v := range vals {
		if v <= 0 {
			b.WriteString(lipgloss.NewStyle().Foreground(cBad).Render("✕"))
			continue
		}
		level := 0
		if max > min {
			level = int((v - min) / (max - min) * float64(len(sparkRunes)-1))
		}
		if level < 0 {
			level = 0
		}
		if level >= len(sparkRunes) {
			level = len(sparkRunes) - 1
		}
		b.WriteString(lipgloss.NewStyle().Foreground(colorRTT(v)).Render(string(sparkRunes[level])))
	}
	return b.String()
}

// bar renders a horizontal gauge of the given fraction (0..1) and color.
func bar(frac float64, width int, color lipgloss.Color) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac * float64(width))
	full := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled))
	empty := lipgloss.NewStyle().Foreground(cMuted).Render(strings.Repeat("░", width-filled))
	return full + empty
}

// labelColor maps a probe quality level to its Russian label and palette colour.
func labelColor(q probe.QualityLevel) (string, lipgloss.Color) {
	switch q {
	case probe.QualityCritical:
		return "Критично", cBad
	case probe.QualityBad:
		return "Плохо", cWarn
	case probe.QualityGood:
		return "Хорошо", cOK
	default:
		return "Отлично", cGood
	}
}

// verdictText returns the rendered "Качество: …" value: either the four-level
// verdict over the rolling window, or a neutral "Собираю данные…" placeholder
// while the window is still too small to be reliable. Using the rolling window
// (not the session-global counters) is what stops a single early-lost packet
// from pinning the verdict at "Плохо" for the rest of the session.
func verdictText(p probe.PingStats) string {
	if p.WindowSize < probe.MinVerdictSamples {
		return lipgloss.NewStyle().Foreground(cDim).Render("Собираю данные…") +
			labelStyle.Render(fmt.Sprintf(" (%d/%d)", p.WindowSize, probe.MinVerdictSamples))
	}
	label, reason, vc := verdict(p.WindowLossPct, probe.Millis(p.WindowJitter))
	out := lipgloss.NewStyle().Bold(true).Foreground(vc).Render(label)
	if reason != "" {
		out += labelStyle.Render(" (" + reason + ")")
	}
	return out
}

// verdict turns a loss%/jitter pair into an overall quality label, colour, and a
// short reason naming the dominant factor (empty when perfect). The severity
// thresholds live in probe.Quality so the TUI and the mobile GUI agree.
func verdict(lossPct, jitterMs float64) (label, reason string, color lipgloss.Color) {
	level, lossDominates := probe.Quality(lossPct, jitterMs)
	label, color = labelColor(level)
	if level > probe.QualityPerfect {
		if lossDominates {
			reason = fmt.Sprintf("потери %.1f%%", lossPct)
		} else {
			reason = fmt.Sprintf("джиттер %.0f ms", jitterMs)
		}
	}
	return label, reason, color
}

// fmtRTT formats a duration as milliseconds, or a dash when zero.
func fmtRTT(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f ms", float64(d)/float64(time.Millisecond))
}

func fmtMbps(f float64) string { return fmt.Sprintf("%.1f Mbps", f) }

// stat renders a small labeled value card.
func stat(label, value string, color lipgloss.Color) string {
	v := lipgloss.NewStyle().Bold(true).Foreground(color).Render(value)
	return boxStyle.Render(labelStyle.Render(label) + "\n" + v)
}
