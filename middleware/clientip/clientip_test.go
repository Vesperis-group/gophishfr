package clientip

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err == nil {
		return p
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("invalid test prefix %q: %v", s, err)
	}
	return netip.PrefixFrom(addr, addr.BitLen())
}

func newResolver(t *testing.T, trusted ...string) *Resolver {
	t.Helper()
	prefixes := make([]netip.Prefix, 0, len(trusted))
	for _, s := range trusted {
		prefixes = append(prefixes, mustPrefix(t, s))
	}
	return NewResolver(prefixes)
}

func newRequest(remoteAddr string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestClientIP_DirectPeer_ForgedXFFIsIgnored(t *testing.T) {
	res := newResolver(t) // trust nobody
	r := newRequest("203.0.113.9:4444", map[string]string{
		"X-Forwarded-For": "1.1.1.1",
	})
	got := res.ClientIP(r)
	if got != "203.0.113.9" {
		t.Fatalf("expected real socket peer, got %q", got)
	}
}

func TestClientIP_DirectPeer_ForgedXRealIPIsIgnored(t *testing.T) {
	res := newResolver(t)
	r := newRequest("203.0.113.9:4444", map[string]string{
		"X-Real-IP": "1.1.1.1",
	})
	got := res.ClientIP(r)
	if got != "203.0.113.9" {
		t.Fatalf("expected real socket peer, got %q", got)
	}
}

func TestClientIP_UntrustedProxyPeer_XFFIgnored(t *testing.T) {
	// 198.51.100.1 is NOT in the trusted list.
	res := newResolver(t, "10.0.0.0/8")
	r := newRequest("198.51.100.1:5555", map[string]string{
		"X-Forwarded-For": "6.6.6.6",
	})
	got := res.ClientIP(r)
	if got != "198.51.100.1" {
		t.Fatalf("expected untrusted proxy's own peer, got %q", got)
	}
}

func TestClientIP_TrustedPeer_RightmostUntrustedHopSelected(t *testing.T) {
	// Simulates the project's own nginx: proxy_add_x_forwarded_for appends
	// the real client to the right of anything the client itself sent.
	res := newResolver(t, "127.0.0.1/32")
	r := newRequest("127.0.0.1:12345", map[string]string{
		"X-Forwarded-For": "6.6.6.6, 203.0.113.42",
	})
	got := res.ClientIP(r)
	if got != "203.0.113.42" {
		t.Fatalf("expected rightmost untrusted hop, got %q", got)
	}
}

func TestClientIP_MultiHopTrustedChain(t *testing.T) {
	res := newResolver(t, "10.0.0.1/32", "10.0.0.2/32")
	r := newRequest("10.0.0.2:1", map[string]string{
		// client -> trustedA (10.0.0.1) -> trustedB (10.0.0.2) -> app
		"X-Forwarded-For": "198.51.100.77, 10.0.0.1, 10.0.0.2",
	})
	got := res.ClientIP(r)
	if got != "198.51.100.77" {
		t.Fatalf("expected first untrusted hop scanning from the right, got %q", got)
	}
}

func TestClientIP_MalformedXFF_FallsBackToPeer(t *testing.T) {
	res := newResolver(t, "127.0.0.1/32")
	cases := []string{
		"not-an-ip",
		"",
		"example.com",
		"203.0.113.5:1234",
		"203.0.113.5,,127.0.0.1",
		"2001:db8::1, 203.0.113.5, not-an-ip",
		"::ffff:203.0.113.5, 1.2.3.4.5",
	}
	for _, xff := range cases {
		r := newRequest("127.0.0.1:1", map[string]string{"X-Forwarded-For": xff})
		got := res.ClientIP(r)
		if got != "127.0.0.1" {
			t.Fatalf("xff=%q: expected safe fallback to peer, got %q", xff, got)
		}
	}
}

func TestClientIP_XRealIP_UntrustedPeerIgnored(t *testing.T) {
	res := newResolver(t, "127.0.0.1/32")
	r := newRequest("198.51.100.5:1", map[string]string{"X-Real-IP": "9.9.9.9"})
	got := res.ClientIP(r)
	if got != "198.51.100.5" {
		t.Fatalf("expected peer, got %q", got)
	}
}

func TestClientIP_XRealIP_TrustedPeer_NoXFF_SingleValidIP(t *testing.T) {
	res := newResolver(t, "127.0.0.1/32")
	r := newRequest("127.0.0.1:1", map[string]string{"X-Real-IP": "203.0.113.5"})
	got := res.ClientIP(r)
	if got != "203.0.113.5" {
		t.Fatalf("expected X-Real-IP value, got %q", got)
	}
}

func TestClientIP_XRealIP_NotConsultedWhenXFFPresent(t *testing.T) {
	// XFF present but entirely unusable (all hops trusted): must not fall
	// back to X-Real-IP, only to peer.
	res := newResolver(t, "127.0.0.1/32")
	r := newRequest("127.0.0.1:1", map[string]string{
		"X-Forwarded-For": "127.0.0.1",
		"X-Real-IP":       "203.0.113.5",
	})
	got := res.ClientIP(r)
	if got != "127.0.0.1" {
		t.Fatalf("expected peer fallback (X-Real-IP must not be consulted), got %q", got)
	}
}

func TestClientIP_XRealIP_MultipleValuesRejected(t *testing.T) {
	res := newResolver(t, "127.0.0.1/32")
	r := newRequest("127.0.0.1:1", nil)
	r.Header.Add("X-Real-IP", "203.0.113.5")
	r.Header.Add("X-Real-IP", "203.0.113.6")
	got := res.ClientIP(r)
	if got != "127.0.0.1" {
		t.Fatalf("expected peer fallback for ambiguous X-Real-IP, got %q", got)
	}
}

func TestClientIP_OversizedXFFFallsBackToPeer(t *testing.T) {
	res := newResolver(t, "127.0.0.1/32")
	huge := make([]byte, maxForwardedForHeaderBytes+1)
	for i := range huge {
		huge[i] = '1'
	}
	r := newRequest("127.0.0.1:1", map[string]string{"X-Forwarded-For": string(huge)})
	got := res.ClientIP(r)
	if got != "127.0.0.1" {
		t.Fatalf("expected peer fallback for oversized header, got %q", got)
	}
}

func TestClientIP_IPv6AndIPv4Mapped(t *testing.T) {
	res := newResolver(t, "::1/128")
	r := newRequest("[::1]:1", map[string]string{
		"X-Forwarded-For": "::ffff:203.0.113.9",
	})
	got := res.ClientIP(r)
	if got != "203.0.113.9" {
		t.Fatalf("expected normalized IPv4-mapped IPv6 address, got %q", got)
	}
}

func TestClientIP_CentralSpoofBypassProof_SameClientSameBucket(t *testing.T) {
	res := newResolver(t, "127.0.0.1/32")
	first := newRequest("127.0.0.1:1", map[string]string{
		"X-Forwarded-For": "9.9.9.9, 203.0.113.9",
	})
	second := newRequest("127.0.0.1:1", map[string]string{
		"X-Forwarded-For": "8.8.8.8, 203.0.113.9",
	})
	gotFirst := res.ClientIP(first)
	gotSecond := res.ClientIP(second)
	if gotFirst != gotSecond {
		t.Fatalf("expected same resolved client IP regardless of forged leftmost hop, got %q and %q", gotFirst, gotSecond)
	}
	if gotFirst != "203.0.113.9" {
		t.Fatalf("expected real client IP, got %q", gotFirst)
	}
}

func TestClientIP_DirectAttacker_CannotChangeBucketAcrossRequests(t *testing.T) {
	res := newResolver(t) // trust nobody: this attacker is not behind any proxy.
	ips := make(map[string]bool)
	for _, forged := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		r := newRequest("203.0.113.1:1", map[string]string{"X-Forwarded-For": forged})
		ips[res.ClientIP(r)] = true
	}
	if len(ips) != 1 {
		t.Fatalf("expected exactly one resolved bucket identity, got %d: %v", len(ips), ips)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	t.Run("empty is trust-none", func(t *testing.T) {
		prefixes, err := ParseTrustedProxies(nil)
		if err != nil || prefixes != nil {
			t.Fatalf("expected nil, nil got %v, %v", prefixes, err)
		}
	})

	t.Run("single IP normalized to /32", func(t *testing.T) {
		prefixes, err := ParseTrustedProxies([]string{"127.0.0.1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(prefixes) != 1 || prefixes[0].String() != "127.0.0.1/32" {
			t.Fatalf("expected [127.0.0.1/32], got %v", prefixes)
		}
	})

	t.Run("single IPv6 normalized to /128", func(t *testing.T) {
		prefixes, err := ParseTrustedProxies([]string{"::1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(prefixes) != 1 || prefixes[0].String() != "::1/128" {
			t.Fatalf("expected [::1/128], got %v", prefixes)
		}
	})

	t.Run("CIDR accepted", func(t *testing.T) {
		prefixes, err := ParseTrustedProxies([]string{"10.0.0.0/8"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(prefixes) != 1 {
			t.Fatalf("expected one prefix, got %v", prefixes)
		}
	})

	t.Run("invalid entry rejected", func(t *testing.T) {
		for _, bad := range []string{"not-an-ip", "", "example.com", "10.0.0.0/99", "10.0.0.0/8/8"} {
			if _, err := ParseTrustedProxies([]string{bad}); err == nil {
				t.Fatalf("expected error for %q", bad)
			}
		}
	})
}

func TestTrustedProxies_UnmarshalJSON(t *testing.T) {
	var tp TrustedProxies
	if err := tp.UnmarshalJSON([]byte(`["127.0.0.1", "10.0.0.0/8"]`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tp) != 2 {
		t.Fatalf("expected 2 prefixes, got %d", len(tp))
	}

	var bad TrustedProxies
	if err := bad.UnmarshalJSON([]byte(`["not-an-ip"]`)); err == nil {
		t.Fatalf("expected error for invalid entry")
	}

	var empty TrustedProxies
	if err := empty.UnmarshalJSON([]byte(`[]`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if empty != nil {
		t.Fatalf("expected nil (trust-none) for empty array, got %v", empty)
	}
}

func TestMiddleware_RewritesRemoteAddrForDownstream(t *testing.T) {
	res := newResolver(t, "127.0.0.1/32")
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	})
	handler := res.Middleware(next)

	r := newRequest("127.0.0.1:1", map[string]string{
		"X-Forwarded-For": "6.6.6.6, 203.0.113.42",
	})
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if seen != "203.0.113.42:0" {
		t.Fatalf("expected rewritten RemoteAddr, got %q", seen)
	}

	// Idempotence: re-running resolution against the already-rewritten
	// RemoteAddr must yield the same result, since the resolved client is
	// not itself a trusted proxy.
	again := res.ClientIP(r)
	if again != "203.0.113.42" {
		t.Fatalf("expected idempotent re-resolution, got %q", again)
	}
}

func TestMiddleware_DirectPeer_NotRewrittenFromForgedHeader(t *testing.T) {
	res := newResolver(t)
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	})
	handler := res.Middleware(next)

	r := newRequest("203.0.113.1:4444", map[string]string{
		"X-Forwarded-For": "1.1.1.1",
	})
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "203.0.113.1:0" {
		t.Fatalf("expected RemoteAddr rewritten to real peer only, got %q", seen)
	}
}
