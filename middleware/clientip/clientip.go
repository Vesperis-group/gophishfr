// Package clientip resolves the real-world client IP address of an HTTP
// request behind zero or more trusted reverse proxies, without ever trusting
// forwarding headers from an untrusted peer.
//
// This package replaces the previously unconditional
// github.com/gorilla/handlers.ProxyHeaders, which rewrote r.RemoteAddr from
// X-Forwarded-For/X-Real-IP for every request regardless of who sent it. That
// meant a direct, non-proxied attacker could set their own X-Forwarded-For
// header and change the IP GophishFR believed they were connecting from on
// every request, bypassing anything (such as the /login rate limiter) that
// keyed state off of the client IP.
//
// See docs/TRUSTED_PROXY_RESOLUTION.md for the full semantics, configuration
// syntax and the right-to-left X-Forwarded-For trust algorithm in plain
// language.
package clientip

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// maxForwardedForHeaderBytes bounds the size of the X-Forwarded-For header
// value that will be parsed, so a single oversized header cannot force
// unbounded CPU/memory work. This is generous for any legitimate deployment
// (even a chain of dozens of hops) while still being a hard, cheap-to-check
// limit.
const maxForwardedForHeaderBytes = 4096

// maxForwardedForHops bounds the number of comma-separated hops that will be
// evaluated, for the same reason as maxForwardedForHeaderBytes. Only the
// rightmost hops are kept, since resolution always scans from the right.
const maxForwardedForHops = 64

// TrustedProxies is a JSON-configurable list of trusted proxy networks. Each
// configured entry is either a single IP address (normalized to a /32 or
// /128 prefix) or a CIDR block. An absent or empty configuration means "trust
// no proxy": X-Forwarded-For and X-Real-IP are then always ignored and the
// resolved client IP is always the direct socket peer.
//
// Unmarshalling an invalid entry (an unparsable IP/CIDR, a hostname, etc.)
// fails immediately with a descriptive error, so a configuration mistake is a
// load-time failure rather than a silently-ignored or silently-permissive
// value.
type TrustedProxies []netip.Prefix

// UnmarshalJSON implements json.Unmarshaler. The wire format is a JSON array
// of strings, each either a bare IP address or a CIDR block.
func (t *TrustedProxies) UnmarshalJSON(data []byte) error {
	var raw []string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	prefixes, err := ParseTrustedProxies(raw)
	if err != nil {
		return err
	}
	*t = prefixes
	return nil
}

// ParseTrustedProxies parses a list of trusted-proxy configuration entries.
// Each entry must be a single IP address or a CIDR block; anything else
// (including an empty string, a hostname, or a malformed value) is rejected
// with a descriptive error that does not echo back attacker-reachable input
// (configuration values are operator-controlled, but the same parser backs
// JSON config loading, so it is kept strict and side-effect free regardless).
//
// A nil or empty list returns a nil, nil result: "trust no proxy".
func ParseTrustedProxies(entries []string) ([]netip.Prefix, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, raw := range entries {
		prefix, err := parseTrustedProxyEntry(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy entry %q: %w", raw, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func parseTrustedProxyEntry(raw string) (netip.Prefix, error) {
	entry := strings.TrimSpace(raw)
	if entry == "" {
		return netip.Prefix{}, errors.New("entry must not be empty")
	}
	if prefix, err := netip.ParsePrefix(entry); err == nil {
		return normalizePrefix(prefix)
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, errors.New("not a valid IP address or CIDR block")
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// normalizePrefix collapses an IPv4-mapped IPv6 CIDR (e.g. "::ffff:10.0.0.0/104")
// into its equivalent plain-IPv4 prefix, so that comparisons against
// already-unmapped request addresses behave as expected. A prefix that spans
// outside the IPv4-mapped range is masked and returned unchanged.
func normalizePrefix(prefix netip.Prefix) (netip.Prefix, error) {
	addr := prefix.Addr()
	bits := prefix.Bits()
	if addr.Is4In6() && bits >= 96 {
		addr = addr.Unmap()
		bits -= 96
	}
	if bits < 0 || bits > addr.BitLen() {
		return netip.Prefix{}, errors.New("invalid prefix length")
	}
	return netip.PrefixFrom(addr, bits).Masked(), nil
}

// Resolver resolves client IPs for requests arriving through zero or more
// explicitly trusted proxies. The zero value trusts no proxy, which is the
// safe default.
type Resolver struct {
	trusted []netip.Prefix
}

// NewResolver returns a Resolver that trusts exactly the given prefixes. A
// nil or empty slice means "trust no proxy".
func NewResolver(trusted []netip.Prefix) *Resolver {
	return &Resolver{trusted: trusted}
}

func (res *Resolver) isTrusted(addr netip.Addr) bool {
	if res == nil || !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range res.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// peerAddr extracts the immediate TCP socket peer from r.RemoteAddr, parsed
// BEFORE any header-based resolution. It returns the parsed address (invalid
// if it could not be parsed as an IP) and a string suitable for use as a
// rate-limit/identity key in all cases.
//
// r.RemoteAddr is set by the Go HTTP server directly from the accepted
// connection, in "host:port" form; it is never derived from a client-supplied
// header by anything in this codebase (this package replaces the one piece of
// code, gorilla/handlers.ProxyHeaders, that used to do that). A SplitHostPort
// failure here therefore indicates an unusual listener/test harness, not an
// attacker-controlled value: the raw string is used only as an opaque
// fallback identity key and is never matched against the trusted-proxy list,
// so it can never cause an incorrect trust decision.
func peerAddr(remoteAddr string) (netip.Addr, string) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, host
	}
	addr = addr.Unmap()
	return addr, addr.String()
}

// ClientIP resolves the real-world client IP for r.
//
// Resolution algorithm (see docs/TRUSTED_PROXY_RESOLUTION.md for the full
// rationale):
//
//  1. peer is the immediate socket peer (r.RemoteAddr), parsed before any
//     header is consulted.
//  2. If peer is not a configured trusted proxy, X-Forwarded-For and
//     X-Real-IP are ignored completely and the result is peer.
//  3. If peer is trusted, X-Forwarded-For (if present at all) is parsed as a
//     comma-separated hop list and evaluated right-to-left: the first hop
//     (starting from the rightmost) that is not itself a trusted proxy is
//     the result. If every hop is trusted, or any hop is malformed, parsing
//     stops and the result safely falls back to peer.
//  4. X-Real-IP is only consulted when X-Forwarded-For is absent entirely
//     (not merely unusable), peer is trusted, and X-Real-IP carries exactly
//     one syntactically valid IP.
//  5. If no usable, trust-consistent hop can be determined, the result is
//     always peer. The resolved value is never derived from an untrusted
//     source.
func (res *Resolver) ClientIP(r *http.Request) string {
	peer, peerKey := peerAddr(r.RemoteAddr)
	if !res.isTrusted(peer) {
		return peerKey
	}

	if xffValues := r.Header.Values("X-Forwarded-For"); len(xffValues) > 0 {
		xff := strings.Join(xffValues, ",")
		if len(xff) <= maxForwardedForHeaderBytes {
			if client, ok := res.resolveForwardedFor(xff); ok {
				return client
			}
		}
		// X-Forwarded-For was present but unusable (oversized, malformed, or
		// every hop trusted). Per the binding algorithm, X-Real-IP is never
		// consulted when X-Forwarded-For is present at all; fall back to peer.
		return peerKey
	}

	if realValues := r.Header.Values("X-Real-IP"); len(realValues) == 1 {
		if client, ok := resolveRealIP(realValues[0]); ok {
			return client
		}
	}
	return peerKey
}

// resolveForwardedFor evaluates a raw X-Forwarded-For value right-to-left,
// returning the first hop (scanning from the rightmost) that is not itself a
// trusted proxy. It bounds the number of hops it will evaluate.
func (res *Resolver) resolveForwardedFor(raw string) (string, bool) {
	hops := strings.Split(raw, ",")
	if len(hops) > maxForwardedForHops {
		// Only the rightmost hops matter: resolution always scans from the
		// right, so dropping the excess leftmost (oldest/least-trusted)
		// entries bounds the work without changing the outcome for any
		// legitimately-sized chain.
		hops = hops[len(hops)-maxForwardedForHops:]
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		addr, ok := parseHopAddr(hop)
		if !ok {
			// A malformed hop anywhere in the chain means the rest of the
			// chain cannot be trusted to mean what it claims. Stop and fail
			// safe rather than guess.
			return "", false
		}
		if res.isTrusted(addr) {
			continue
		}
		return addr.String(), true
	}
	// Every hop was itself a trusted proxy (or the list was empty): there is
	// no untrusted hop to treat as the client.
	return "", false
}

// parseHopAddr parses a single X-Forwarded-For hop. It is intentionally
// strict: a bare IP literal is required, so hostnames, empty elements, and
// values carrying an unexpected port suffix (e.g. "203.0.113.5:1234") are all
// rejected rather than guessed at.
func parseHopAddr(hop string) (netip.Addr, bool) {
	if hop == "" {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(hop)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// resolveRealIP parses an X-Real-IP header value, which must be exactly one
// syntactically valid IP literal.
func resolveRealIP(raw string) (string, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	return addr.Unmap().String(), true
}

// Middleware returns an http.Handler wrapper that resolves the client IP for
// each request using ClientIP and rewrites r.RemoteAddr to that resolved
// value (with a synthetic zero port, preserving the "host:port" shape that
// net.SplitHostPort-based callers such as access logging already expect).
//
// This is the safe drop-in replacement for the removed
// gorilla/handlers.ProxyHeaders: unlike it, the rewritten value is only ever
// derived from a forwarding header when the immediate socket peer is an
// explicitly configured trusted proxy. Applying this middleware is idempotent:
// re-resolving an already-resolved r.RemoteAddr is a no-op, since the
// resolved client address is (by construction) not itself a configured
// trusted proxy.
func (res *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = net.JoinHostPort(res.ClientIP(r), "0")
		next.ServeHTTP(w, r)
	})
}
