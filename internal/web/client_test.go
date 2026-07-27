package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// local builds a client that may reach the test server on loopback. Every
// other test in this file that exercises the guard uses New(Options{}).
func local() *Client { return New(Options{AllowPrivateAddresses: true}) }

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return parsed
}

func get(t *testing.T, c *Client, raw string) (Page, error) {
	t.Helper()
	target, err := ParseTarget(raw)
	if err != nil {
		t.Fatalf("ParseTarget(%q): %v", raw, err)
	}
	return c.Get(t.Context(), target)
}

func TestClientRefusesLoopbackWithoutTheTestEscapeHatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("secret internal service"))
	}))
	defer server.Close()

	_, err := get(t, New(Options{}), server.URL)
	if err == nil {
		t.Fatal("the default client reached a loopback service; the guard is not wired into the transport")
	}
	var blocked *BlockedAddressError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected a BlockedAddressError, got %T: %v", err, err)
	}
}

func TestClientFollowsSameSiteRedirectsAndReportsCrossSiteOnes(t *testing.T) {
	var origin *httptest.Server
	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hop":
			http.Redirect(w, r, origin.URL+"/landed", http.StatusFound)
		case "/landed":
			w.Write([]byte("same site landing"))
		case "/offsite":
			// A different hostname for the same loopback address is exactly
			// the shape that matters: the port is irrelevant, the host is not.
			elsewhere := *mustParse(t, origin.URL)
			elsewhere.Host = "localhost:" + elsewhere.Port()
			http.Redirect(w, r, elsewhere.String(), http.StatusFound)
		}
	}))
	defer origin.Close()

	client := local()
	page, err := get(t, client, origin.URL+"/hop")
	if err != nil {
		t.Fatalf("same-site redirect was not followed: %v", err)
	}
	if !strings.Contains(string(page.Body), "same site landing") {
		t.Fatalf("body=%q", page.Body)
	}
	if page.URL == page.RequestedURL {
		t.Fatal("the final URL should record where the redirect landed")
	}

	_, err = get(t, client, origin.URL+"/offsite")
	var crossSite *CrossSiteRedirectError
	if !errors.As(err, &crossSite) {
		t.Fatalf("a cross-site redirect must be reported, not followed; got %T: %v", err, err)
	}
	if !strings.Contains(crossSite.Error(), "Nothing was fetched") {
		t.Errorf("the message must say nothing was fetched: %s", crossSite)
	}
}

func TestSameSiteAcceptsWWWAndSubdomainsButNotRedirectors(t *testing.T) {
	same := [][2]string{
		{"example.com", "example.com"},
		{"example.com", "www.example.com"},
		{"www.example.com", "example.com"},
		{"docs.example.com", "api.docs.example.com"},
		{"example.com.", "example.com"},
	}
	for _, pair := range same {
		if !sameSite(pair[0], pair[1]) {
			t.Errorf("%s -> %s should be same-site", pair[0], pair[1])
		}
	}
	different := [][2]string{
		{"t.co", "example.com"},
		{"example.com", "example.com.evil.test"},
		{"example.com", "notexample.com"},
		{"bit.ly", "169.254.169.254"},
	}
	for _, pair := range different {
		if sameSite(pair[0], pair[1]) {
			t.Errorf("%s -> %s must not be treated as same-site", pair[0], pair[1])
		}
	}
}

func TestClientBoundsResponseSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer server.Close()

	client := New(Options{AllowPrivateAddresses: true, MaxBytes: 100})
	page, err := get(t, client, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Body) != 100 || !page.Truncated {
		t.Fatalf("body=%d truncated=%v; the cap was not applied", len(page.Body), page.Truncated)
	}
}

func TestClientRefusesAnOversizedDeclaredLengthWithoutReadingIt(t *testing.T) {
	served := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.Header().Set("Content-Length", "999999999")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
	}))
	defer server.Close()

	_, err := get(t, New(Options{AllowPrivateAddresses: true, MaxBytes: 1024}), server.URL)
	if err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("an oversized declared length must be refused, got %v (served=%v)", err, served)
	}
}

func TestParseTargetValidatesAndSanitizesModelSuppliedURLs(t *testing.T) {
	if _, err := ParseTarget(""); err == nil {
		t.Error("an empty url must be rejected")
	}
	for _, raw := range []string{"file:///etc/passwd", "javascript:alert(1)", "ftp://example.com/x", "data:text/html,hi", "gopher://example.com"} {
		if _, err := ParseTarget(raw); err == nil {
			t.Errorf("%q must be rejected: web_fetch reads http(s) only", raw)
		}
	}
	bare, err := ParseTarget("example.com/docs")
	if err != nil || bare.Scheme != "https" || bare.Host != "example.com" {
		t.Fatalf("a bare host should become https: %+v %v", bare, err)
	}
	credentialed, err := ParseTarget("https://user:secret@example.com/x")
	if err != nil {
		t.Fatal(err)
	}
	if credentialed.User != nil || strings.Contains(credentialed.String(), "secret") {
		t.Fatalf("URL credentials must be stripped, not sent: %s", credentialed)
	}
}

func TestClientIgnoresProxyEnvironment(t *testing.T) {
	// A proxy inherited from the environment would carry model-chosen
	// requests to a host the address guard never saw.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("direct"))
	}))
	defer server.Close()

	page, err := get(t, local(), server.URL)
	if err != nil || string(page.Body) != "direct" {
		t.Fatalf("the request did not go direct: body=%q err=%v", page.Body, err)
	}
}

func TestEveryRequestPresentsTheSameDesktopBrowserIdentity(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("User-Agent"))
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "/landed", http.StatusFound)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	// A search, the fetch it leads to, a redirect hop inside that fetch, and a
	// second client entirely must all present one identity. A failure that
	// depends on which string was drawn is the most expensive shape a bug can
	// have, which is why there is nothing to draw from.
	for _, path := range []string{"/a", "/hop", "/b"} {
		if _, err := get(t, local(), server.URL+path); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("expected 4 requests including the redirect hop, got %d", len(seen))
	}
	for _, agent := range seen {
		if agent != UserAgent {
			t.Fatalf("request presented %q, want the single identity %q", agent, UserAgent)
		}
	}
}

func TestUserAgentIsAPlausibleDesktopBrowser(t *testing.T) {
	if !strings.HasPrefix(UserAgent, "Mozilla/5.0 (") || strings.ContainsAny(UserAgent, "\n\r") {
		t.Fatalf("malformed user agent %q", UserAgent)
	}
	// Mobile identities are served a different, usually smaller document, and
	// these tools exist to read the whole article.
	for _, mobile := range []string{"iPhone", "Android", "Mobile", "iPad"} {
		if strings.Contains(UserAgent, mobile) {
			t.Errorf("user agent claims a mobile device (%q): %s", mobile, UserAgent)
		}
	}
	if strings.Contains(UserAgent, "Collomia") || strings.Contains(UserAgent, "Go-http-client") {
		t.Errorf("user agent is not a browser string: %s", UserAgent)
	}
}

func TestUserAgentCanBeOverriddenForTests(t *testing.T) {
	client := New(Options{AllowPrivateAddresses: true, UserAgent: "Fixture/1.0"})
	if client.UserAgent() != "Fixture/1.0" {
		t.Fatalf("overridden user agent = %q", client.UserAgent())
	}
	if New(Options{}).UserAgent() != UserAgent {
		t.Fatal("the default client must present the package identity")
	}
}

func TestPostFormReachesTheServerAsAQuery(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		got = r.Form.Get("q")
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	target, _ := url.Parse(server.URL)
	if _, err := local().PostForm(t.Context(), target, url.Values{"q": {"hello world"}}); err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Fatalf("server saw q=%q", got)
	}
}

func TestClientNegotiatesHTTP11EvenWhenTheServerOffersHTTP2(t *testing.T) {
	// Go's HTTP/2 client sends a distinctive SETTINGS frame that bot
	// management products fingerprint. Measured against Stack Overflow, the
	// same request was 403 over HTTP/2 and 200 over HTTP/1.1 with no other
	// difference. Restoring HTTP/2 would silently re-block those sites, so the
	// protocol is pinned here rather than left to negotiation.
	var protos []string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protos = append(protos, r.Proto)
		w.Write([]byte("ok"))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	// The test server uses a self-signed certificate, so this exercises
	// protocol negotiation through a transport that trusts it.
	client := New(Options{AllowPrivateAddresses: true})
	transport := client.http.Transport.(*http.Transport)
	transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()

	if _, err := get(t, client, server.URL); err != nil {
		t.Fatal(err)
	}
	if len(protos) != 1 || protos[0] != "HTTP/1.1" {
		t.Fatalf("server saw %v, want one HTTP/1.1 request", protos)
	}
}
