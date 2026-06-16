//go:build darwin || linux

package probe

import (
	"fmt"
	"net"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// protoICMP is the IANA protocol number for ICMPv4, used when parsing replies.
const protoICMP = 1

// reply is a parsed ICMP message addressed to one of our probes. seq is the
// sequence number of the original echo (recovered from the embedded packet for
// TimeExceeded/Unreachable), which is how we match replies to requests — on
// Darwin the kernel rewrites the ICMP ID, so the ID cannot be trusted.
type reply struct {
	kind replyKind
	peer net.IP
	seq  int
}

// icmpConn wraps an unprivileged datagram ICMP socket plus its IPv4 view (for
// per-packet TTL control used by the tracer).
type icmpConn struct {
	c  *icmp.PacketConn
	p  *ipv4.PacketConn
	id int
}

// openICMP opens an unprivileged ("udp4") ICMP socket. This works without root
// on macOS and Linux (incl. Android); the kernel demultiplexes replies to the
// right socket. The x/net/icmp docs note this datagram mode is supported only
// on Darwin and Linux — hence the build tag and the separate Windows backend.
func openICMP() (*icmpConn, error) {
	c, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		return nil, fmt.Errorf("open icmp socket: %w", err)
	}
	return &icmpConn{c: c, p: c.IPv4PacketConn(), id: 0xffff}, nil
}

func (cn *icmpConn) close() error         { return cn.c.Close() }
func (cn *icmpConn) setTTL(ttl int) error { return cn.p.SetTTL(ttl) }

// sendEcho sends an ICMP echo request to dst carrying the given sequence number.
func (cn *icmpConn) sendEcho(dst net.IP, seq int, payload []byte) error {
	wm := icmp.Message{
		Type: ipv4.ICMPTypeEcho, Code: 0,
		Body: &icmp.Echo{ID: cn.id, Seq: seq, Data: payload},
	}
	wb, err := wm.Marshal(nil)
	if err != nil {
		return err
	}
	_, err = cn.c.WriteTo(wb, &net.UDPAddr{IP: dst})
	return err
}

// readReply blocks until one ICMP message arrives or deadline passes. It returns
// ok=false on timeout. Stray/garbled packets are returned as replyOther so the
// caller can ignore them and keep reading.
func (cn *icmpConn) readReply(deadline time.Time) (r reply, ok bool, err error) {
	buf := make([]byte, 1500)
	if e := cn.c.SetReadDeadline(deadline); e != nil {
		return reply{}, false, e
	}
	n, peer, e := cn.c.ReadFrom(buf)
	if e != nil {
		if ne, isNet := e.(net.Error); isNet && ne.Timeout() {
			return reply{}, false, nil // plain timeout, not an error
		}
		return reply{}, false, e
	}
	r.peer = addrIP(peer)
	msg, e := icmp.ParseMessage(protoICMP, buf[:n])
	if e != nil {
		r.kind = replyOther
		return r, true, nil
	}
	switch body := msg.Body.(type) {
	case *icmp.Echo:
		r.kind = replyEcho
		r.seq = body.Seq
	case *icmp.TimeExceeded:
		r.kind = replyTimeExceeded
		r.seq = innerSeq(body.Data)
	case *icmp.DstUnreach:
		r.kind = replyUnreachable
		r.seq = innerSeq(body.Data)
	default:
		r.kind = replyOther
	}
	return r, true, nil
}

// addrIP extracts the IP from a datagram-socket peer address (*net.UDPAddr).
func addrIP(a net.Addr) net.IP {
	if u, ok := a.(*net.UDPAddr); ok {
		return u.IP
	}
	if i, ok := a.(*net.IPAddr); ok {
		return i.IP
	}
	return nil
}

// innerSeq recovers the sequence number of the original echo from the payload of
// a TimeExceeded/Unreachable message, which carries the offending IP packet
// (header + at least its first 8 bytes = the ICMP echo header).
func innerSeq(data []byte) int {
	if len(data) < 1 {
		return -1
	}
	ihl := int(data[0]&0x0f) * 4
	inner := data[ihl:]
	if len(inner) < 8 {
		return -1
	}
	return int(inner[6])<<8 | int(inner[7])
}

// unixProber implements prober over one unprivileged datagram ICMP socket.
type unixProber struct {
	conn    *icmpConn
	payload []byte
}

func newProber() (prober, error) {
	c, err := openICMP()
	if err != nil {
		return nil, err
	}
	return &unixProber{conn: c, payload: []byte("nettest-probe")}, nil
}

func (u *unixProber) close() error { return u.conn.close() }

func (u *unixProber) probe(dst net.IP, ttl, seq int, timeout time.Duration) (probeResult, bool, error) {
	if err := u.conn.setTTL(ttl); err != nil {
		return probeResult{}, false, err
	}
	start := time.Now()
	if err := u.conn.sendEcho(dst, seq, u.payload); err != nil {
		return probeResult{}, false, err
	}
	deadline := start.Add(timeout)
	for {
		r, ok, err := u.conn.readReply(deadline)
		if err != nil {
			return probeResult{}, false, err
		}
		if !ok {
			return probeResult{}, false, nil // timeout → no reply
		}
		if r.seq == seq {
			return probeResult{kind: r.kind, peer: r.peer, rtt: time.Since(start)}, true, nil
		}
		// A stray/late reply for a different seq — keep reading until the deadline.
	}
}
