package probe

import (
	"math"
	"testing"
	"time"
)

func TestWindowStats_Empty(t *testing.T) {
	size, loss, jit := windowStats(nil, 30)
	if size != 0 || loss != 0 || jit != 0 {
		t.Errorf("empty hist: got (%d, %v, %v), want all zero", size, loss, jit)
	}
}

func TestWindowStats_LossPctOverWindowNotSession(t *testing.T) {
	// The reported case: one lost probe (the first) followed by 54 good ones.
	// Session-global loss is 1/55 ≈ 1.8% — keeps "Плохо" stuck. The window of
	// the last 30 must report 0% because the lost probe has rolled out.
	hist := []float64{0} // initial loss
	for range 54 {
		hist = append(hist, 15) // 54 successful probes
	}
	size, loss, _ := windowStats(hist, 30)
	if size != 30 {
		t.Errorf("size = %d, want 30 (capped)", size)
	}
	if loss != 0 {
		t.Errorf("loss = %v, want 0 — the lost probe should have rolled out", loss)
	}
}

func TestWindowStats_LossWhileWithinWindow(t *testing.T) {
	// Loss is still inside the window: 2 out of 30 lost → 6.67%.
	hist := []float64{0, 0}
	for range 28 {
		hist = append(hist, 15)
	}
	size, loss, _ := windowStats(hist, 30)
	if size != 30 {
		t.Errorf("size = %d, want 30", size)
	}
	want := 2.0 / 30 * 100
	if math.Abs(loss-want) > 0.01 {
		t.Errorf("loss = %v, want ~%v", loss, want)
	}
}

func TestWindowStats_SizeLessThanCap(t *testing.T) {
	// Fewer probes than the window cap — size reflects what we have.
	hist := []float64{15, 16, 17, 0, 18}
	size, loss, _ := windowStats(hist, 30)
	if size != 5 {
		t.Errorf("size = %d, want 5", size)
	}
	want := 1.0 / 5 * 100
	if math.Abs(loss-want) > 0.01 {
		t.Errorf("loss = %v, want %v", loss, want)
	}
}

func TestWindowStats_JitterFromSuccessiveRTTs(t *testing.T) {
	// |16-15| + |14-16| + |15-14| = 1+2+1 = 4 over 3 diffs = ~1.33ms.
	hist := []float64{15, 16, 14, 15}
	_, _, jit := windowStats(hist, 30)
	want := 4.0 / 3
	got := float64(jit) / float64(time.Millisecond)
	if math.Abs(got-want) > 0.1 {
		t.Errorf("jitter = %v ms, want %v ms", got, want)
	}
}

func TestWindowStats_JitterIgnoresLosses(t *testing.T) {
	// A lost probe between two successful ones must not create a fake jitter
	// spike — we compute jitter only over the surviving RTTs.
	hist := []float64{15, 0, 15}
	_, _, jit := windowStats(hist, 30)
	if jit != 0 {
		t.Errorf("jitter = %v, want 0 (two equal RTTs around a loss)", jit)
	}
}

func TestWindowStats_AllLost(t *testing.T) {
	hist := []float64{0, 0, 0, 0, 0}
	size, loss, jit := windowStats(hist, 30)
	if size != 5 || loss != 100 || jit != 0 {
		t.Errorf("all-loss: got (%d, %v, %v), want (5, 100, 0)", size, loss, jit)
	}
}
