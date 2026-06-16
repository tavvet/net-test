//go:build windows

package probe

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no unprivileged datagram ICMP socket, so we use the iphlpapi ICMP
// helper API (the same one ping.exe/tracert.exe use). IcmpSendEcho is a
// synchronous request/reply that needs no administrator rights and reports the
// responding host even for TTL-expired hops — so both ping and traceroute work.
var (
	modIphlpapi         = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile  = modIphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = modIphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho    = modIphlpapi.NewProc("IcmpSendEcho")
)

// IP_STATUS codes returned in icmpEchoReply.Status.
const (
	ipSuccess           = 0
	ipDestNetUnreach    = 11002
	ipDestHostUnreach   = 11003
	ipDestProtoUnreach  = 11004
	ipDestPortUnreach   = 11005
	ipReqTimedOut       = 11010
	ipTTLExpiredTransit = 11013
)

// ipOptionInformation mirrors Win32 IP_OPTION_INFORMATION; we use it to set TTL.
type ipOptionInformation struct {
	TTL         uint8
	TOS         uint8
	Flags       uint8
	OptionsSize uint8
	OptionsData *byte
}

// icmpEchoReply mirrors Win32 ICMP_ECHO_REPLY. Field order/padding match the C
// layout on both 386 and amd64 (pointer-sized fields auto-size per arch).
type icmpEchoReply struct {
	Address       uint32 // IPAddr of the responder (network byte order)
	Status        uint32
	RoundTripTime uint32
	DataSize      uint16
	Reserved      uint16
	Data          uintptr
	Options       ipOptionInformation
}

type windowsProber struct {
	handle  uintptr
	payload []byte
	reply   []byte
}

func newProber() (prober, error) {
	h, _, _ := procIcmpCreateFile.Call()
	if h == 0 || h == uintptr(windows.InvalidHandle) {
		return nil, fmt.Errorf("IcmpCreateFile failed")
	}
	return &windowsProber{
		handle:  h,
		payload: []byte("nettest-probe"),
		reply:   make([]byte, 512), // ICMP_ECHO_REPLY header + payload + slack
	}, nil
}

func (w *windowsProber) close() error {
	procIcmpCloseHandle.Call(w.handle)
	return nil
}

func (w *windowsProber) probe(dst net.IP, ttl, seq int, timeout time.Duration) (probeResult, bool, error) {
	ip4 := dst.To4()
	if ip4 == nil {
		return probeResult{}, false, fmt.Errorf("only IPv4 is supported")
	}
	destAddr := binary.LittleEndian.Uint32(ip4) // IPAddr: first octet in the low byte

	opt := ipOptionInformation{TTL: uint8(ttl)}
	millis := uint32(timeout / time.Millisecond)
	if millis == 0 {
		millis = 1000
	}

	n, _, callErr := procIcmpSendEcho.Call(
		w.handle,
		uintptr(destAddr),
		uintptr(unsafe.Pointer(&w.payload[0])),
		uintptr(uint16(len(w.payload))),
		uintptr(unsafe.Pointer(&opt)),
		uintptr(unsafe.Pointer(&w.reply[0])),
		uintptr(uint32(len(w.reply))),
		uintptr(millis),
	)
	if n == 0 {
		// No reply was written. GetLastError (callErr) distinguishes a normal
		// timeout (IP_REQ_TIMED_OUT) — reported as ok=false, no error — from a
		// real failure like a bad handle or bad parameter, which we surface so
		// the user sees the cause instead of silent "100% loss".
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 && uintptr(errno) != ipReqTimedOut {
			return probeResult{}, false, fmt.Errorf("IcmpSendEcho: %w", callErr)
		}
		return probeResult{}, false, nil // timeout / no reply
	}

	rep := (*icmpEchoReply)(unsafe.Pointer(&w.reply[0]))
	peer := make(net.IP, 4)
	binary.LittleEndian.PutUint32(peer, rep.Address)
	res := probeResult{peer: peer, rtt: time.Duration(rep.RoundTripTime) * time.Millisecond}

	switch rep.Status {
	case ipSuccess:
		res.kind = replyEcho
	case ipTTLExpiredTransit:
		res.kind = replyTimeExceeded
	case ipDestNetUnreach, ipDestHostUnreach, ipDestProtoUnreach, ipDestPortUnreach:
		res.kind = replyUnreachable
	case ipReqTimedOut:
		return probeResult{}, false, nil
	default:
		res.kind = replyOther
	}
	return res, true, nil
}
