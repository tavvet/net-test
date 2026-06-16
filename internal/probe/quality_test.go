package probe

import "testing"

func TestQuality(t *testing.T) {
	tests := []struct {
		name         string
		loss, jitter float64
		level        QualityLevel
		lossDom      bool
	}{
		{"clean", 0, 0, QualityPerfect, true},
		{"jitter-at-good-boundary", 0, 8, QualityPerfect, true}, // 8 is not > 8
		{"jitter-good", 0, 8.1, QualityGood, false},
		{"loss-good", 0.5, 0, QualityGood, true},
		{"loss-bad", 2, 0, QualityBad, true},
		{"loss-critical", 6, 0, QualityCritical, true},
		{"jitter-dominates-bad", 1, 25, QualityBad, false},
		{"jitter-critical-dominates", 2, 60, QualityCritical, false},
		{"both-critical-loss-wins", 6, 60, QualityCritical, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			level, lossDom := Quality(tt.loss, tt.jitter)
			if level != tt.level || lossDom != tt.lossDom {
				t.Errorf("Quality(%v, %v) = (%v, %v), want (%v, %v)",
					tt.loss, tt.jitter, level, lossDom, tt.level, tt.lossDom)
			}
		})
	}
}
