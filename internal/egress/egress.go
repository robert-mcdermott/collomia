// Package egress brokers outbound network access for sandboxed commands.
//
// The OS sandbox can already deny remote egress outright, but that
// all-or-nothing switch is the first thing a user turns off when a build needs
// a package registry. This package narrows it into something a user can leave
// on: the sandbox denies direct remote traffic while leaving loopback open,
// and the only remaining route out is a Collomia-owned proxy on loopback that
// dials nothing except the destinations policy already allows.
//
// The broker reads its destination from the proxy request itself — the CONNECT
// authority, or an absolute-form request URI — and dials exactly that host. It
// never inspects or terminates TLS; an approved tunnel is spliced byte for
// byte. That is also why no SNI parsing appears here: because the broker dials
// the host it was given, a client cannot name one destination and reach
// another, so the CONNECT authority is already authoritative.
//
// What this package provides is enforcement only in combination with a sandbox
// that denies direct remote egress. On its own it is a cooperative control that
// binds proxy-aware tools and nothing else. Only macOS Seatbelt currently
// supplies the other half; see [Supported].
package egress

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/policy"
	"github.com/robert-mcdermott/collomia/internal/shell"
)

// dialTimeout bounds how long an approved destination may take to accept a
// connection. A build waiting on an unreachable registry should fail with the
// command's own timeout rather than holding a broker goroutine indefinitely.
const dialTimeout = 30 * time.Second

// Allowlist decides which destinations the broker will dial. It is built from
// the host-scoped allow rules already in the permission configuration, so
// enabling scoped egress adds no second place to describe reachable hosts.
type Allowlist struct {
	patterns []string
}

// NewAllowlist normalizes and de-duplicates host patterns. Patterns are
// matched with the same glob the policy layer applies to Rule.Host, so
// "*.example.com" covers a subdomain and "example.com" does not.
func NewAllowlist(patterns []string) Allowlist {
	seen := make(map[string]struct{}, len(patterns))
	out := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if _, ok := seen[pattern]; ok {
			continue
		}
		seen[pattern] = struct{}{}
		out = append(out, pattern)
	}
	sort.Strings(out)
	return Allowlist{patterns: out}
}

// Permits reports whether a destination authority may be dialed. An authority
// the host normalizer cannot read exactly is refused rather than guessed at:
// the policy layer treats an unreadable endpoint as uncoverable by an allow
// rule, and the broker must not be more generous than the rule that named it.
func (a Allowlist) Permits(authority string) bool {
	host, ok := shell.NormalizeHost(authority)
	if !ok {
		return false
	}
	for _, pattern := range a.patterns {
		if policy.HostMatches(pattern, host) {
			return true
		}
	}
	return false
}

// Patterns returns the effective allowlist for display and diagnostics.
func (a Allowlist) Patterns() []string { return append([]string(nil), a.patterns...) }

// Empty reports an allowlist that permits nothing. Scoped egress with an empty
// allowlist is a valid, maximally strict state, but it is far more often a
// configuration mistake, so callers surface it rather than letting every
// outbound connection fail with no explanation.
func (a Allowlist) Empty() bool { return len(a.patterns) == 0 }

// FromRules collects the hosts named by allow rules. Only allow rules
// contribute: a prompt or deny rule mentioning a host describes when to ask or
// refuse, not a destination that may be reached without asking.
func FromRules(rules []appconfig.Rule) Allowlist {
	var hosts []string
	for _, rule := range rules {
		if strings.EqualFold(strings.TrimSpace(rule.Action), "allow") && rule.Host != "" {
			hosts = append(hosts, rule.Host)
		}
	}
	return NewAllowlist(hosts)
}

// Decision records one brokered destination for the audit ledger.
type Decision struct {
	// Host is the normalized destination, or the raw authority when it could
	// not be normalized (which is itself a refusal reason).
	Host    string
	Allowed bool
	// Reason explains a refusal in user-facing terms.
	Reason string
}

// Broker is a loopback HTTP proxy that tunnels only allowlisted destinations.
type Broker struct {
	listener net.Listener
	allow    Allowlist
	server   *http.Server
	observe  func(Decision)

	mu        sync.Mutex
	closed    bool
	refused   []string
	permitted []string
}

// Start binds a broker to an ephemeral loopback port and begins serving. The
// caller owns the returned broker and must Close it when the command finishes.
//
// Binding to 127.0.0.1 specifically, rather than to a hostname or all
// interfaces, is part of the contract: the sandbox policy that makes this
// enforcement rather than cooperation permits loopback and nothing else.
func Start(allow Allowlist, observe func(Decision)) (*Broker, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind loopback egress broker: %w", err)
	}
	b := &Broker{listener: listener, allow: allow, observe: observe}
	b.server = &http.Server{Handler: b}
	go func() {
		// A closed listener is the ordinary shutdown path, not a failure.
		if err := b.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return
		}
	}()
	return b, nil
}

// Addr is the broker's loopback address in host:port form.
func (b *Broker) Addr() string { return b.listener.Addr().String() }

// Environ returns the proxy variables a child process needs in order to route
// through the broker.
//
// Both cases are set deliberately. Upper and lower case are both emitted
// because tools disagree about which they read — curl prefers the lowercase
// form, Go's own transport reads either. NO_PROXY is explicitly cleared: an
// inherited NO_PROXY would let a tool skip the broker and attempt a direct
// connection, which the sandbox then denies, turning a policy decision into an
// unexplained connection failure.
func (b *Broker) Environ() []string {
	url := "http://" + b.Addr()
	return []string{
		"HTTP_PROXY=" + url, "http_proxy=" + url,
		"HTTPS_PROXY=" + url, "https_proxy=" + url,
		"ALL_PROXY=" + url, "all_proxy=" + url,
		"NO_PROXY=", "no_proxy=",
	}
}

// Refused returns the distinct destinations this broker declined, so the tool
// layer can explain a failed build in terms of the hosts it would need.
func (b *Broker) Refused() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.refused...)
}

// Permitted returns the distinct destinations this broker dialed.
func (b *Broker) Permitted() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.permitted...)
}

// Close stops the broker. It is safe to call more than once.
func (b *Broker) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()
	return b.server.Close()
}

func (b *Broker) record(d Decision) {
	b.mu.Lock()
	list := &b.permitted
	if !d.Allowed {
		list = &b.refused
	}
	found := false
	for _, existing := range *list {
		if existing == d.Host {
			found = true
			break
		}
	}
	if !found {
		*list = append(*list, d.Host)
	}
	b.mu.Unlock()
	if b.observe != nil {
		b.observe(d)
	}
}

func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		b.serveConnect(w, r)
		return
	}
	b.serveAbsolute(w, r)
}

// serveConnect handles tunnelled traffic, which is every https:// request and
// anything else a proxy-aware tool wraps in CONNECT.
func (b *Broker) serveConnect(w http.ResponseWriter, r *http.Request) {
	authority := r.Host
	if authority == "" {
		authority = r.URL.Host
	}
	if !b.authorize(w, authority) {
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "egress broker cannot tunnel this connection", http.StatusInternalServerError)
		return
	}
	upstream, err := net.DialTimeout("tcp", withDefaultPort(authority, "443"), dialTimeout)
	if err != nil {
		http.Error(w, "egress broker could not reach "+authority+": "+err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	client, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	splice(client, upstream)
}

// serveAbsolute handles a plain http:// request, which a proxy receives in
// absolute form rather than as a tunnel.
func (b *Broker) serveAbsolute(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() {
		// A relative request means something addressed the broker directly
		// rather than using it as a proxy. There is nothing useful to serve.
		http.Error(w, "this port is Collomia's egress broker, not a web server", http.StatusBadRequest)
		return
	}
	if !b.authorize(w, r.URL.Host) {
		return
	}
	outbound := r.Clone(r.Context())
	outbound.RequestURI = ""
	// Hop-by-hop headers belong to the client/broker connection and must not
	// be forwarded to the destination.
	outbound.Header.Del("Proxy-Connection")
	outbound.Header.Del("Proxy-Authorization")
	response, err := http.DefaultTransport.RoundTrip(outbound)
	if err != nil {
		http.Error(w, "egress broker could not reach "+r.URL.Host+": "+err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for key, values := range response.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

// authorize checks one destination and, on refusal, answers with an
// explanation naming the host and the setting that governs it. A tool that
// prints the proxy's response body then tells the user what to change.
func (b *Broker) authorize(w http.ResponseWriter, authority string) bool {
	host, normalized := shell.NormalizeHost(authority)
	if !normalized {
		b.record(Decision{Host: authority, Allowed: false, Reason: "destination could not be read as a hostname"})
		http.Error(w, "egress broker refused an unreadable destination: "+authority, http.StatusForbidden)
		return false
	}
	if !b.allow.Permits(authority) {
		b.record(Decision{Host: host, Allowed: false, Reason: "no host-scoped allow rule covers it"})
		http.Error(w, refusalMessage(host), http.StatusForbidden)
		return false
	}
	b.record(Decision{Host: host, Allowed: true})
	return true
}

func refusalMessage(host string) string {
	return fmt.Sprintf(
		"Collomia's egress broker refused %s: permissions.sandbox_egress is \"scoped\" and no host-scoped allow rule covers it. "+
			"Add {\"action\":\"allow\",\"host\":%q} to permissions.rules, or set permissions.sandbox_egress to \"off\" to restore unscoped command networking.",
		host, host)
}

// withDefaultPort supplies the implied port when a CONNECT authority omits it.
func withDefaultPort(authority, port string) string {
	if _, _, err := net.SplitHostPort(authority); err == nil {
		return authority
	}
	return net.JoinHostPort(authority, port)
}

// splice copies in both directions until either side closes, so a tunnel lives
// exactly as long as the conversation inside it.
func splice(client, upstream net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, client)
		// Half-closing lets the destination see the client's EOF instead of
		// waiting for a response that will never be written.
		if conn, ok := upstream.(*net.TCPConn); ok {
			_ = conn.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, upstream)
		if conn, ok := client.(*net.TCPConn); ok {
			_ = conn.CloseWrite()
		}
	}()
	wg.Wait()
}
