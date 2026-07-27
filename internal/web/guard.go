package web

import (
	"fmt"
	"net"
	"syscall"
)

// This file decides which addresses the built-in web tools may connect to.
//
// A URL the model chose is not a URL the user chose. Left unguarded, a fetch
// tool is a request forger sitting inside the user's network: it can read a
// cloud instance's metadata service, a company intranet, or a service bound to
// loopback that trusts anything that can reach it. None of those are on the
// public web, which is the only thing these tools claim to read.
//
// The check runs on the resolved IP at connect time rather than on the
// hostname, which is what makes it a boundary instead of a heuristic. A name
// that resolves to a public address on the first lookup and a private one on
// the second (DNS rebinding) is refused on the second, and every redirect hop
// opens its own connection and is checked the same way.

// BlockedAddressError explains a refused destination in terms a user can act
// on. It names the address and why it is not reachable, because "connection
// refused" for a deliberate policy decision is the kind of error people spend
// an afternoon on.
type BlockedAddressError struct {
	Address string
	Reason  string
}

func (e *BlockedAddressError) Error() string {
	return fmt.Sprintf("refused to connect to %s: %s. The built-in web tools reach the public internet only; use run_command for anything on this machine or this network.", e.Address, e.Reason)
}

// reservedRanges are the address blocks Go's own classifiers do not cover but
// which are still not the public internet.
var reservedRanges = []struct {
	cidr   string
	reason string
	parsed *net.IPNet
}{
	{cidr: "100.64.0.0/10", reason: "carrier-grade NAT space (RFC 6598)"},
	{cidr: "192.0.0.0/24", reason: "IETF protocol assignments (RFC 6890)"},
	{cidr: "192.0.2.0/24", reason: "documentation range TEST-NET-1"},
	{cidr: "198.18.0.0/15", reason: "network benchmark range (RFC 2544)"},
	{cidr: "198.51.100.0/24", reason: "documentation range TEST-NET-2"},
	{cidr: "203.0.113.0/24", reason: "documentation range TEST-NET-3"},
	{cidr: "240.0.0.0/4", reason: "reserved for future use (RFC 1112)"},
	{cidr: "2001:db8::/32", reason: "IPv6 documentation range"},
	{cidr: "100::/64", reason: "IPv6 discard-only range (RFC 6666)"},
}

// nat64Prefix is the well-known translation prefix. An address inside it
// carries an IPv4 destination in its low four bytes, so a private IPv4 target
// can otherwise be reached through an address that looks globally routable.
var nat64Prefix = net.IPNet{IP: net.ParseIP("64:ff9b::"), Mask: net.CIDRMask(96, 128)}

func init() {
	for i := range reservedRanges {
		_, network, err := net.ParseCIDR(reservedRanges[i].cidr)
		if err != nil {
			panic("web: bad reserved CIDR " + reservedRanges[i].cidr)
		}
		reservedRanges[i].parsed = network
	}
}

// blockedReason reports why an address is not on the public internet, or ""
// when it is reachable.
func blockedReason(ip net.IP) string {
	if ip == nil {
		return "the address could not be read"
	}
	if len(ip) == net.IPv6len && nat64Prefix.Contains(ip) {
		if embedded := net.IPv4(ip[12], ip[13], ip[14], ip[15]); blockedReason(embedded) != "" {
			return "NAT64 translation of the non-public address " + embedded.String()
		}
	}
	// An IPv4-mapped IPv6 address must be classified as the IPv4 address it
	// carries; ::ffff:127.0.0.1 is loopback however it is written.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	switch {
	case ip.IsUnspecified():
		return "it is the unspecified address"
	case ip.IsLoopback():
		return "it is a loopback address on this machine"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return "it is link-local, where cloud instance metadata services live"
	case ip.IsPrivate():
		return "it is a private network address"
	case ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return "it is a multicast address"
	case !ip.IsGlobalUnicast():
		return "it is not a globally routable address"
	}
	for _, reserved := range reservedRanges {
		if reserved.parsed.Contains(ip) {
			return "it is in " + reserved.reason
		}
	}
	return ""
}

// dialControl is installed on the dialer so the check runs against the address
// the connection will actually use. allowPrivate exists for tests, which need
// a loopback HTTP server; nothing reads it from configuration, because a
// setting that turns this guard off is the setting an attacker asks for.
func dialControl(allowPrivate bool) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		if allowPrivate {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return &BlockedAddressError{Address: address, Reason: "the destination could not be read as an address"}
		}
		ip := net.ParseIP(host)
		if ip == nil {
			// The dialer resolves before Control runs, so a non-literal here
			// means something unexpected; refusing is the safe reading.
			return &BlockedAddressError{Address: address, Reason: "the destination did not resolve to an IP address"}
		}
		if reason := blockedReason(ip); reason != "" {
			return &BlockedAddressError{Address: host, Reason: reason}
		}
		return nil
	}
}
