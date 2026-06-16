package probe

import (
	"net"
	"time"
)

// replyKind classifies an ICMP reply relative to a probe we sent.
type replyKind int

const (
	replyOther        replyKind = iota
	replyEcho                   // EchoReply — the destination answered
	replyTimeExceeded           // a router on the path (TTL hit zero)
	replyUnreachable            // destination/port unreachable
)

// probeResult is the outcome of one ICMP echo probe.
type probeResult struct {
	kind replyKind
	peer net.IP // who replied: the target for echo, an intermediate router for time-exceeded
	rtt  time.Duration
}

// prober sends single ICMP echo probes synchronously. Each prober owns its own
// resources and must be used by one goroutine at a time — create several for
// concurrent probing (the tracer does). Implementations are platform-specific:
// unprivileged datagram ICMP sockets on macOS/Linux, the iphlpapi ICMP API on
// Windows. This is the only platform-dependent seam in the codebase.
type prober interface {
	// probe sends one echo to dst with the given IP TTL and waits up to
	// timeout for a reply. ok is false on timeout (no reply within the window).
	// seq is used to match replies where the platform exposes it (Unix);
	// platforms with request/reply APIs (Windows) ignore it.
	probe(dst net.IP, ttl, seq int, timeout time.Duration) (res probeResult, ok bool, err error)
	close() error
}
