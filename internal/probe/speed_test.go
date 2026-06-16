package probe

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestMeasureStreams_NoResRace pumps a short measurement window through
// measureStreams with synthetic streams. The ticker (250ms) is guaranteed to
// fire and read *res concurrently with the finaliser's writes — this is the
// race the F3 fix targets. Run under -race to catch.
func TestMeasureStreams_NoResRace(t *testing.T) {
	ctx := context.Background()
	out := make(chan SpeedProgress, 32)
	res := &SpeedProgress{Phase: PhaseDownload}

	// Drain the output channel so the goroutines never block on send.
	doneDrain := make(chan struct{})
	go func() {
		defer close(doneDrain)
		for range out {
		}
	}()

	// Synthetic stream: dump a megabyte every few ms until ctx is cancelled.
	var bytesSent int64
	fake := func(sctx context.Context, add func(int64)) {
		for sctx.Err() == nil {
			add(1 << 20)
			atomic.AddInt64(&bytesSent, 1<<20)
			time.Sleep(5 * time.Millisecond)
		}
	}

	// 600ms window gives the ticker 2-3 fires AND multiple stream goroutines.
	mbps := measureStreams(ctx, out, res, 600*time.Millisecond, 3, fake)
	close(out)
	<-doneDrain

	if mbps <= 0 {
		t.Errorf("got mbps=%v, want >0 (sanity)", mbps)
	}
	if atomic.LoadInt64(&bytesSent) <= 0 {
		t.Errorf("stream produced no bytes")
	}
	// The race we're catching is detected by -race on the test run itself —
	// passing here under -race means the synchronisation is sound.
}
