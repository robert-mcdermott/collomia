package web

import (
	"net"
	"strings"
	"testing"
)

func TestGuardRefusesEverythingThatIsNotThePublicInternet(t *testing.T) {
	blocked := map[string]string{
		"127.0.0.1":              "loopback",
		"::1":                    "loopback",
		"::ffff:127.0.0.1":       "IPv4-mapped loopback",
		"0.0.0.0":                "unspecified",
		"169.254.169.254":        "cloud instance metadata",
		"fe80::1":                "IPv6 link-local",
		"10.1.2.3":               "RFC 1918",
		"172.16.9.9":             "RFC 1918",
		"192.168.0.1":            "RFC 1918",
		"fd00::1":                "IPv6 unique-local",
		"100.64.1.1":             "carrier-grade NAT",
		"192.0.2.5":              "documentation range",
		"198.18.0.1":             "benchmark range",
		"203.0.113.7":            "documentation range",
		"240.0.0.1":              "reserved",
		"255.255.255.255":        "broadcast",
		"224.0.0.1":              "multicast",
		"2001:db8::1":            "IPv6 documentation",
		"64:ff9b::a01:203":       "NAT64-wrapped 10.1.2.3",
		"64:ff9b::7f00:1":        "NAT64-wrapped loopback",
		"::ffff:169.254.169.254": "IPv4-mapped metadata",
	}
	for address, why := range blocked {
		ip := net.ParseIP(address)
		if ip == nil {
			t.Fatalf("test fixture %q is not an IP", address)
		}
		if reason := blockedReason(ip); reason == "" {
			t.Errorf("%s (%s) was allowed; it must be refused", address, why)
		}
	}

	public := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111", "2001:4860:4860::8888"}
	for _, address := range public {
		ip := net.ParseIP(address)
		if reason := blockedReason(ip); reason != "" {
			t.Errorf("public address %s was refused: %s", address, reason)
		}
	}
}

func TestGuardExplainsRefusalInActionableTerms(t *testing.T) {
	err := dialControl(false)("tcp", "169.254.169.254:80", nil)
	if err == nil {
		t.Fatal("metadata address was not refused")
	}
	var blocked *BlockedAddressError
	if !asBlocked(err, &blocked) {
		t.Fatalf("error was not a BlockedAddressError: %T", err)
	}
	message := blocked.Error()
	for _, want := range []string{"169.254.169.254", "link-local", "run_command"} {
		if !strings.Contains(message, want) {
			t.Errorf("refusal message missing %q: %s", want, message)
		}
	}
}

func TestGuardRefusesUnparseableDestinations(t *testing.T) {
	for _, address := range []string{"not-an-address", "example.com:443"} {
		if err := dialControl(false)("tcp", address, nil); err == nil {
			t.Errorf("%q was allowed; a destination the guard cannot read must be refused", address)
		}
	}
}

func asBlocked(err error, target **BlockedAddressError) bool {
	blocked, ok := err.(*BlockedAddressError)
	if ok {
		*target = blocked
	}
	return ok
}
