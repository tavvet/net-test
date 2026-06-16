package probe

import (
	"math"
	"time"
)

// Thresholds chosen to be conservative — we'd rather miss a marginal problem
// than flag a hop that just rate-limits ICMP. Tuned by feel; can be revisited.
const (
	anomalyLossMinPct = 1.0  // ignore loss below this — within ICMP noise
	anomalyLossTolPct = 1.5  // tail hops may show slightly less loss than the suspect
	anomalyRTTDeltaMs = 15.0 // hop-to-hop AvgRTT jump needed to even consider RTT anomaly
	anomalyRTTTolMs   = 5.0  // tail hops may flutter a bit below the suspect's AvgRTT
)

// markAnomalies fills DeltaRTT and the *Persists flags on each hop using
// persistent-vs-transient heuristics:
//
//   - LossPersists: hop i has loss > anomalyLossMinPct AND every later hop also
//     shows loss within anomalyLossTolPct of hop i. A single hop with loss while
//     later hops are clean is almost always ICMP rate-limiting on that router,
//     not a real problem on the path — so we don't flag it.
//   - RTTPersists: DeltaRTT > anomalyRTTDeltaMs (a real jump from the previous
//     hop) AND every later hop's AvgRTT stays within anomalyRTTTolMs of this
//     hop's AvgRTT — i.e. the added latency does not "disappear" further down,
//     which would betray a transient spike.
//
// The function modifies hops in place; it does not need or care about cycle
// state, so it can run after each trace snapshot. Cost is O(n).
func markAnomalies(hops []Hop) {
	n := len(hops)
	if n == 0 {
		return
	}

	// tailMinLoss[i] = min loss across hops[i..n-1].
	// tailMinRTT[i]  = min AvgRTT(ms) across hops[i..n-1] that have replies;
	//                  +Inf if no later hop has replies (i.e. unknown).
	tailMinLoss := make([]float64, n)
	tailMinRTT := make([]float64, n)
	tailMinLoss[n-1] = hops[n-1].LossPct
	tailMinRTT[n-1] = rttMs(hops[n-1])
	for i := n - 2; i >= 0; i-- {
		tailMinLoss[i] = math.Min(tailMinLoss[i+1], hops[i].LossPct)
		r := rttMs(hops[i])
		tailMinRTT[i] = math.Min(tailMinRTT[i+1], r)
	}

	var prevAvg time.Duration
	for i := range hops {
		h := &hops[i]

		// Δ vs the previous responding hop. Silent hops (AvgRTT == 0) don't
		// reset prevAvg — otherwise one missing hop would erase Δ on the next.
		if h.AvgRTT > 0 {
			if i > 0 && prevAvg > 0 {
				h.DeltaRTT = h.AvgRTT - prevAvg
			}
			prevAvg = h.AvgRTT
		}

		// LossPersists: hop has real loss AND tail does too (within tolerance).
		if h.LossPct > anomalyLossMinPct {
			// tail = hops[i+1..n-1]; for the last hop the tail is empty and the
			// loss is by definition "persistent" (it's the destination).
			tailMin := math.Inf(1)
			if i+1 < n {
				tailMin = tailMinLoss[i+1]
			}
			if i+1 == n || tailMin >= h.LossPct-anomalyLossTolPct {
				h.LossPersists = true
			}
		}

		// RTTPersists: large hop-to-hop jump AND no recovery on later hops.
		if Millis(h.DeltaRTT) > anomalyRTTDeltaMs && i+1 < n {
			myAvg := rttMs(*h)
			if tailMinRTT[i+1] >= myAvg-anomalyRTTTolMs {
				h.RTTPersists = true
			}
		}
	}
}

// rttMs returns AvgRTT in milliseconds, or +Inf if the hop has no replies
// (so it doesn't accidentally pull down the tail min).
func rttMs(h Hop) float64 {
	if h.Recv == 0 || h.AvgRTT == 0 {
		return math.Inf(1)
	}
	return Millis(h.AvgRTT)
}
