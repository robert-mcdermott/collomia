package egress

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

func TestAllowlistPermits(t *testing.T) {
	allow := NewAllowlist([]string{"proxy.golang.org", "*.example.com", "  REGISTRY.NPMJS.ORG  "})
	cases := []struct {
		authority string
		want      bool
		why       string
	}{
		{"proxy.golang.org", true, "exact match"},
		{"proxy.golang.org:443", true, "the port is not part of the comparison"},
		{"PROXY.GOLANG.ORG", true, "hostnames compare case-insensitively"},
		{"registry.npmjs.org", true, "the pattern is normalized on entry"},
		{"api.example.com", true, "a wildcard covers a subdomain"},
		{"example.com", false, "*.example.com does not cover the bare domain"},
		// filepath.Match's * matches any run of non-separator bytes, and the
		// separator is /, not a dot. A host wildcard therefore spans labels.
		// This is the policy layer's existing Rule.Host semantic and the broker
		// shares its matcher precisely so the two cannot disagree.
		{"deep.api.example.com", true, "a host wildcard spans dots, covering nested subdomains"},
		{"evil.com", false, "no pattern covers it"},
		{"evil.com:443", false, "a port does not smuggle a host past the allowlist"},
		{"proxy.golang.org.evil.com", false, "a suffix that merely contains an allowed host is not that host"},
		{"$HOST", false, "an authority that cannot be read exactly is refused, not guessed"},
		{"", false, "an empty authority is refused"},
	}
	for _, tc := range cases {
		if got := allow.Permits(tc.authority); got != tc.want {
			t.Errorf("Permits(%q) = %v, want %v (%s)", tc.authority, got, tc.want, tc.why)
		}
	}
}

func TestAllowlistEmptyPermitsNothing(t *testing.T) {
	allow := NewAllowlist(nil)
	if !allow.Empty() {
		t.Fatal("an allowlist built from no patterns must report Empty")
	}
	if allow.Permits("proxy.golang.org") {
		t.Error("an empty allowlist must permit nothing rather than everything")
	}
}

func TestFromRulesTakesOnlyAllowHosts(t *testing.T) {
	allow := FromRules([]appconfig.Rule{
		{Action: "allow", Host: "proxy.golang.org"},
		{Action: "allow", Host: "*.example.com"},
		{Action: "prompt", Host: "prompted.example.org"},
		{Action: "deny", Host: "denied.example.org"},
		{Action: "allow", Command: "go"},
		{Action: "allow", Host: "proxy.golang.org"},
	})
	got := strings.Join(allow.Patterns(), ",")
	if want := "*.example.com,proxy.golang.org"; got != want {
		t.Fatalf("allowlist = %q, want %q", got, want)
	}
	if allow.Permits("prompted.example.org") {
		t.Error("a prompt rule describes when to ask, not a destination reachable without asking")
	}
	if allow.Permits("denied.example.org") {
		t.Error("a deny rule must never contribute to the allowlist")
	}
}

func TestEnvironClearsInheritedNoProxy(t *testing.T) {
	broker := startTestBroker(t, NewAllowlist([]string{"example.com"}))
	env := broker.Environ()
	joined := strings.Join(env, "\n")
	for _, key := range []string{"HTTP_PROXY=", "http_proxy=", "HTTPS_PROXY=", "https_proxy=", "ALL_PROXY=", "all_proxy="} {
		if !strings.Contains(joined, key+"http://"+broker.Addr()) {
			t.Errorf("Environ is missing %s pointing at the broker", key)
		}
	}
	// An inherited NO_PROXY would route a tool around the broker and into a
	// sandbox denial, which reads as an unexplained connection failure.
	for _, want := range []string{"NO_PROXY=", "no_proxy="} {
		found := false
		for _, entry := range env {
			if entry == want {
				found = true
			}
		}
		if !found {
			t.Errorf("Environ must clear %s explicitly", strings.TrimSuffix(want, "="))
		}
	}
}

func TestBrokerForwardsAllowedPlainHTTP(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "reached the destination")
	}))
	defer destination.Close()

	broker := startTestBroker(t, NewAllowlist([]string{"127.0.0.1"}))
	body, status := throughProxy(t, broker, destination.URL)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body != "reached the destination" {
		t.Fatalf("body = %q, want the destination's response", body)
	}
	if got := broker.Permitted(); len(got) != 1 || got[0] != "127.0.0.1" {
		t.Errorf("Permitted() = %v, want [127.0.0.1]", got)
	}
}

func TestBrokerRefusesUnlistedPlainHTTP(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the destination must never be reached for a refused host")
	}))
	defer destination.Close()

	// The allowlist names a different host, so 127.0.0.1 is not covered.
	broker := startTestBroker(t, NewAllowlist([]string{"proxy.golang.org"}))
	body, status := throughProxy(t, broker, destination.URL)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	// The refusal has to be actionable: a build that fails this way should
	// print the host and the setting that governs it.
	for _, want := range []string{"127.0.0.1", "sandbox_egress", "permissions.rules"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal body %q does not mention %q", body, want)
		}
	}
	if got := broker.Refused(); len(got) != 1 || got[0] != "127.0.0.1" {
		t.Errorf("Refused() = %v, want [127.0.0.1]", got)
	}
}

func TestBrokerTunnelsAllowedConnect(t *testing.T) {
	destination := echoListener(t)
	broker := startTestBroker(t, NewAllowlist([]string{"127.0.0.1"}))

	conn := dialBroker(t, broker)
	defer conn.Close()
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", destination, destination)
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("CONNECT response = %q, want 200", status)
	}
	// Drain the blank line that ends the response head, then use the tunnel.
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read CONNECT headers: %v", err)
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	fmt.Fprint(conn, "ping\n")
	echoed, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read through tunnel: %v", err)
	}
	if strings.TrimSpace(echoed) != "ping" {
		t.Fatalf("tunnel echoed %q, want \"ping\"", strings.TrimSpace(echoed))
	}
}

func TestBrokerRefusesUnlistedConnect(t *testing.T) {
	broker := startTestBroker(t, NewAllowlist([]string{"proxy.golang.org"}))
	conn := dialBroker(t, broker)
	defer conn.Close()
	fmt.Fprint(conn, "CONNECT evil.example.com:443 HTTP/1.1\r\nHost: evil.example.com:443\r\n\r\n")
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if !strings.Contains(status, "403") {
		t.Fatalf("CONNECT response = %q, want 403", status)
	}
	if got := broker.Refused(); len(got) != 1 || got[0] != "evil.example.com" {
		t.Errorf("Refused() = %v, want [evil.example.com]", got)
	}
}

func TestBrokerRecordsDecisionsForAudit(t *testing.T) {
	var seen []Decision
	broker, err := Start(NewAllowlist([]string{"proxy.golang.org"}), func(d Decision) { seen = append(seen, d) })
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer broker.Close()

	conn := dialBroker(t, broker)
	fmt.Fprint(conn, "CONNECT blocked.example.com:443 HTTP/1.1\r\nHost: blocked.example.com:443\r\n\r\n")
	_, _ = bufio.NewReader(conn).ReadString('\n')
	conn.Close()

	if len(seen) != 1 {
		t.Fatalf("observed %d decisions, want 1", len(seen))
	}
	if seen[0].Allowed || seen[0].Host != "blocked.example.com" || seen[0].Reason == "" {
		t.Errorf("decision = %+v, want a refusal for blocked.example.com carrying a reason", seen[0])
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	broker, err := Start(NewAllowlist(nil), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := broker.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := broker.Close(); err != nil {
		t.Fatalf("second Close must be a no-op, got %v", err)
	}
}

func startTestBroker(t *testing.T, allow Allowlist) *Broker {
	t.Helper()
	broker, err := Start(allow, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	return broker
}

func dialBroker(t *testing.T, broker *Broker) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", broker.Addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	return conn
}

// throughProxy issues a plain HTTP request via the broker and returns the body
// and status the client saw.
func throughProxy(t *testing.T, broker *Broker, target string) (string, int) {
	t.Helper()
	proxyURL, err := url.Parse("http://" + broker.Addr())
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}
	response, err := client.Get(target)
	if err != nil {
		t.Fatalf("request through broker: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return strings.TrimSpace(string(body)), response.StatusCode
}

// echoListener starts a line echo server and returns its host:port.
func echoListener(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return listener.Addr().String()
}
