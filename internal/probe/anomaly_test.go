package probe

import (
	"testing"
	"time"
)

// mk builds a Hop with the only fields markAnomalies inspects.
func mk(lossPct, avgMs float64) Hop {
	return Hop{
		LossPct: lossPct,
		AvgRTT:  time.Duration(avgMs * float64(time.Millisecond)),
		Recv:    1, // anything > 0 — so rttMs treats AvgRTT as known
	}
}

func TestMarkAnomalies_TransientLossIgnored(t *testing.T) {
	// Hop 2 shows 8% loss but later hops are clean → ICMP rate-limiting, not a path issue.
	hops := []Hop{mk(0, 10), mk(8, 12), mk(0, 13), mk(0, 14)}
	markAnomalies(hops)
	for i, h := range hops {
		if h.LossPersists {
			t.Errorf("hop %d: LossPersists = true, want false (transient loss should be ignored)", i)
		}
	}
}

func TestMarkAnomalies_PersistentLossFlagged(t *testing.T) {
	// Hop 2 introduces 4% loss that all subsequent hops also see → real problem at hop 2.
	hops := []Hop{mk(0, 10), mk(0, 12), mk(4, 13), mk(4, 14), mk(5, 15)}
	markAnomalies(hops)
	if hops[2].LossPersists != true {
		t.Errorf("hop 2: LossPersists = false, want true (loss propagates to end)")
	}
	for _, i := range []int{0, 1} {
		if hops[i].LossPersists {
			t.Errorf("hop %d: LossPersists = true, want false", i)
		}
	}
}

func TestMarkAnomalies_TransientRTTSpikeIgnored(t *testing.T) {
	// Hop 2 has +50ms over hop 1, but hop 3 returns to baseline → spike on hop 2 alone.
	hops := []Hop{mk(0, 10), mk(0, 12), mk(0, 62), mk(0, 13), mk(0, 14)}
	markAnomalies(hops)
	for i, h := range hops {
		if h.RTTPersists {
			t.Errorf("hop %d: RTTPersists = true, want false (transient spike)", i)
		}
	}
}

func TestMarkAnomalies_PersistentRTTRiseFlagged(t *testing.T) {
	// Hop 2 introduces +35ms; subsequent hops stay near that level → real added latency.
	hops := []Hop{mk(0, 10), mk(0, 12), mk(0, 47), mk(0, 49), mk(0, 50)}
	markAnomalies(hops)
	if !hops[2].RTTPersists {
		t.Errorf("hop 2: RTTPersists = false, want true (RTT rise propagates)")
	}
	for _, i := range []int{0, 1, 3, 4} {
		if hops[i].RTTPersists {
			t.Errorf("hop %d: RTTPersists = true, want false", i)
		}
	}
}

func TestMarkAnomalies_DeltaRTT(t *testing.T) {
	hops := []Hop{mk(0, 10), mk(0, 14), mk(0, 50)}
	markAnomalies(hops)
	if got, want := hops[0].DeltaRTT, time.Duration(0); got != want {
		t.Errorf("hop 0 DeltaRTT = %v, want %v (no prev hop)", got, want)
	}
	if got, want := hops[1].DeltaRTT, 4*time.Millisecond; got != want {
		t.Errorf("hop 1 DeltaRTT = %v, want %v", got, want)
	}
	if got, want := hops[2].DeltaRTT, 36*time.Millisecond; got != want {
		t.Errorf("hop 2 DeltaRTT = %v, want %v", got, want)
	}
}

func TestMarkAnomalies_LastHopLossIsPersistent(t *testing.T) {
	// Loss at the destination has no "tail" to compare with → must still flag.
	hops := []Hop{mk(0, 10), mk(0, 12), mk(3, 14)}
	markAnomalies(hops)
	if !hops[2].LossPersists {
		t.Errorf("last hop with loss: LossPersists = false, want true")
	}
}

func TestMarkAnomalies_NoRepliesYet(t *testing.T) {
	// A non-responding hop must not poison the tail-min computations.
	hops := []Hop{
		mk(0, 10),
		{LossPct: 100, Recv: 0}, // hop 2 silent
		mk(0, 60),               // hop 3 looks slow…
		mk(0, 62),
	}
	markAnomalies(hops)
	// hop 3's RTT jump is +50ms over the LAST RESPONDING hop (10ms), and persists →
	// flagged. This is correct — the silent hop just doesn't break the analysis.
	if !hops[2].RTTPersists {
		t.Errorf("hop 2 (after silent hop): RTTPersists = false, want true")
	}
}
