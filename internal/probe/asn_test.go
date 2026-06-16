package probe

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReverseIPv4(t *testing.T) {
	for in, want := range map[string]string{
		"5.180.172.2": "2.172.180.5",
		"8.8.8.8":     "8.8.8.8",
		"1.2.3.4":     "4.3.2.1",
	} {
		if got := reverseIPv4(in); got != want {
			t.Errorf("reverseIPv4(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitCymru(t *testing.T) {
	in := "13335 | US | arin | 2010-07-14 | CLOUDFLARENET - Cloudflare, Inc., US"
	got := splitCymru(in)
	want := []string{"13335", "US", "arin", "2010-07-14", "CLOUDFLARENET - Cloudflare, Inc., US"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%q)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestIsLocalIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
		why  string
	}{
		{"10.0.1.1", true, "RFC1918"},
		{"192.168.1.1", true, "RFC1918"},
		{"172.16.0.1", true, "RFC1918"},
		{"127.0.0.1", true, "loopback"},
		{"169.254.1.1", true, "link-local"},
		{"100.64.0.1", true, "CGNAT"},
		{"100.127.255.254", true, "CGNAT upper bound"},
		{"100.128.0.0", false, "just outside CGNAT"},
		{"100.63.255.255", false, "just below CGNAT"},
		{"8.8.8.8", false, "public"},
		{"1.1.1.1", false, "public"},
	}
	for _, c := range cases {
		got := isLocalIP(net.ParseIP(c.ip))
		if got != c.want {
			t.Errorf("isLocalIP(%s) = %v, want %v (%s)", c.ip, got, c.want, c.why)
		}
	}
}

func TestASNCache_PrivateIPSkipped(t *testing.T) {
	// Private IPs must be cached as empty without a DNS query. We can verify
	// this just by checking the cache state — no need to mock the resolver.
	c := newASNCache(context.Background())
	got := c.lookup("10.0.1.1")
	if got.num != "" || got.name != "" {
		t.Errorf("private IP returned %+v, want empty", got)
	}
	// "Pending" must be empty: a private IP must not have triggered a lookup.
	c.mu.Lock()
	pending := len(c.pending)
	cached, hasCache := c.results["10.0.1.1"]
	c.mu.Unlock()
	if pending != 0 {
		t.Errorf("pending = %d, want 0 (private IPs must not trigger lookups)", pending)
	}
	if !hasCache || cached.num != "" {
		t.Errorf("private IP not cached as empty: cached=%+v, hasCache=%v", cached, hasCache)
	}
}

func TestASNCache_RepeatedLookupsDeduplicated(t *testing.T) {
	// A second lookup for an IP whose first lookup is still pending must
	// return the cached empty value without enqueuing a new goroutine.
	c := newASNCache(context.Background())
	// Manually mark "1.2.3.4" as pending to simulate an in-flight resolve.
	c.mu.Lock()
	c.pending["1.2.3.4"] = struct{}{}
	c.mu.Unlock()

	got := c.lookup("1.2.3.4")
	if got.num != "" || got.name != "" {
		t.Errorf("pending lookup returned %+v, want empty", got)
	}
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 1 {
		t.Errorf("pending = %d, want 1 (no new goroutine should be enqueued)", pending)
	}
}

func TestASNCache_ConcurrentLookupsRaceFree(t *testing.T) {
	// Hit the cache from many goroutines at once: private IPs only, so no
	// real DNS happens. With -race this would catch any unprotected map access.
	c := newASNCache(context.Background())
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			c.lookup("10.0.0.1")
			c.lookup("192.168.1.1")
		})
	}
	wg.Wait()
}

// TestASNCache_LiveLookup hits the real Team Cymru DNS and is gated by
// NETTEST_LIVE — same convention as the live ICMP test. Confirms end-to-end
// resolution works.
func TestASNCache_LiveLookup(t *testing.T) {
	if os.Getenv("NETTEST_LIVE") == "" {
		t.Skip("set NETTEST_LIVE=1 to run the live network test")
	}
	c := newASNCache(context.Background())
	c.lookup("1.1.1.1") // triggers background resolve
	deadline := time.Now().Add(5 * time.Second)
	var info asnInfo
	for time.Now().Before(deadline) {
		info = c.lookup("1.1.1.1")
		if info.num != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if info.num == "" {
		t.Fatal("ASN lookup for 1.1.1.1 returned empty after 5s")
	}
	if info.num != "AS13335" {
		t.Errorf("ASN = %q, want AS13335 (Cloudflare)", info.num)
	}
	if !strings.Contains(strings.ToUpper(info.name), "CLOUDFLARE") {
		t.Errorf("ASName = %q, want to contain CLOUDFLARE", info.name)
	}
	t.Logf("1.1.1.1 → %s / %s", info.num, info.name)
}
