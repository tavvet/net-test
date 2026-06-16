package probe

// QualityLevel is a link-quality severity, from best to worst, derived from the
// rolling-window loss and jitter. It is pure analytics: the UI layer maps a
// level to a label/colour, so the thresholds live in one place and the TUI and
// the mobile GUI can't drift apart.
type QualityLevel int

const (
	QualityPerfect QualityLevel = iota
	QualityGood
	QualityBad
	QualityCritical
)

// Quality classifies a (loss%, jitter-ms) pair as the worse of the two
// per-factor severities, and reports whether loss is the dominant factor (so a
// caller can name the reason). Absolute RTT is intentionally ignored — a stable
// link to a distant host is fine; only loss and jitter judge quality.
func Quality(lossPct, jitterMs float64) (level QualityLevel, lossDominates bool) {
	lq, jq := lossLevel(lossPct), jitterLevel(jitterMs)
	if lq >= jq {
		return lq, true
	}
	return jq, false
}

func lossLevel(pct float64) QualityLevel {
	switch {
	case pct > 5:
		return QualityCritical
	case pct > 1:
		return QualityBad
	case pct > 0:
		return QualityGood
	default:
		return QualityPerfect
	}
}

func jitterLevel(ms float64) QualityLevel {
	switch {
	case ms > 50:
		return QualityCritical
	case ms > 20:
		return QualityBad
	case ms > 8:
		return QualityGood
	default:
		return QualityPerfect
	}
}
