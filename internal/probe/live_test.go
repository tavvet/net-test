package probe

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLivePingAndTraceConcurrent runs the ping monitor and the tracer at the
// same time (two unprivileged ICMP sockets) and checks both collect real
// replies — i.e. the kernel demuxes between the sockets correctly. Hits the
// network, so it is skipped unless NETTEST_LIVE=1.
func TestLivePingAndTraceConcurrent(t *testing.T) {
	if os.Getenv("NETTEST_LIVE") == "" {
		t.Skip("set NETTEST_LIVE=1 to run the live network test")
	}
	ip, label, err := Resolve("1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	pch := make(chan PingStats, 8)
	tch := make(chan TraceSnapshot, 8)
	go NewPinger(ip, label, 500*time.Millisecond, 2*time.Second).Run(ctx, pch)
	go NewTracer(ip, label, 30, time.Second, 2*time.Second).Run(ctx, tch)

	var lp PingStats
	var lt TraceSnapshot
	for done := false; !done; {
		select {
		case s := <-pch:
			lp = s
		case s := <-tch:
			lt = s
		case <-ctx.Done():
			done = true
		}
	}

	if lp.Sent == 0 || lp.Recv == 0 {
		t.Errorf("ping got no replies (sent=%d recv=%d) — concurrent socket demux?", lp.Sent, lp.Recv)
	}
	hopReplies := 0
	for _, h := range lt.Hops {
		if h.Recv > 0 {
			hopReplies++
		}
	}
	if hopReplies == 0 {
		t.Errorf("trace got no hop replies (%d hops) — concurrent socket demux?", len(lt.Hops))
	}
	t.Logf("ping recv=%d/%d loss=%.0f%%; trace hops=%d with replies=%d",
		lp.Recv, lp.Sent, lp.LossPct, len(lt.Hops), hopReplies)
}
