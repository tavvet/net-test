package probe

import (
	"context"
	"testing"
	"time"
)

// TestDNSCache_PendClearedAfterResolve verifies that dnsCache.resolve removes
// the ip from d.pend after writing the name — otherwise pend grows monotonically
// and a once-failed lookup would never be retried.
//
// Uses 127.0.0.1: reverse-DNS is fast and predictable on every host; we don't
// care what name comes back, only that pend is cleaned.
func TestDNSCache_PendClearedAfterResolve(t *testing.T) {
	d := newDNSCache(context.Background())
	d.lookup("127.0.0.1") // enqueues background resolve, adds to pend

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		_, stillPending := d.pend["127.0.0.1"]
		_, gotName := d.names["127.0.0.1"]
		d.mu.Unlock()
		if !stillPending && gotName {
			return // resolve completed AND removed itself from pend
		}
		time.Sleep(10 * time.Millisecond)
	}
	d.mu.Lock()
	pend := len(d.pend)
	d.mu.Unlock()
	t.Errorf("pend not cleared 3s after resolve (size=%d)", pend)
}

// TestDNSCache_ParentCtxCancelsResolve verifies that cancelling the parent
// context aborts the in-flight resolve quickly (≤ 200ms) instead of waiting
// for the 2-second internal timeout. This was the F2 leak path.
func TestDNSCache_ParentCtxCancelsResolve(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	d := newDNSCache(parent)

	// 192.0.2.1 is TEST-NET-1 (RFC 5737) — reverse lookup will likely time
	// out at 2s; with parent cancellation we expect resolve to bail much sooner.
	d.lookup("192.0.2.1")

	time.Sleep(50 * time.Millisecond) // let the goroutine enter LookupAddr
	cancel()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		_, stillPending := d.pend["192.0.2.1"]
		d.mu.Unlock()
		if !stillPending {
			return // resolve aborted on parent cancel
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("resolve didn't bail out within 500ms of parent cancellation")
}
