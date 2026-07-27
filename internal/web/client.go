// Package web provides the built-in, configuration-free web search and page
// retrieval behind Collomia's web_search and web_fetch tools.
//
// Searching and reading the web is not an optional integration for a coding
// agent — a library's current API, a error message nobody has seen before, a
// changelog — so it is built in rather than left to an MCP server the user has
// to find, install, and trust. That decision carries an obligation: the
// capability ships with no API key, no endpoint to configure, and no setting
// that turns its safety guards off.
//
// Three properties define what this package is:
//
//   - It reaches the public internet only. See guard.go; the check runs on the
//     resolved address at connect time, and there is no way to disable it.
//   - It returns text, bounded. A response is capped on the wire, HTML is
//     reduced to readable text, and anything that is not text is refused with
//     its type and size rather than inlined.
//   - Everything it returns is external data. Callers frame results through
//     internal/external so a page cannot present itself as instructions.
package web

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultMaxBytes bounds one response on the wire. Pages far larger than
	// this exist, but the useful text in them does not.
	DefaultMaxBytes = 5 << 20
	// DefaultTimeout bounds a single retrieval end to end.
	DefaultTimeout = 30 * time.Second
	// maxRedirects bounds a redirect chain that stays on one site.
	maxRedirects = 5
)

// UserAgent is the single identity every request presents: desktop Chrome on
// Windows, the most common browser string on the web.
//
// Presenting a browser at all is what makes these tools usable. A great many
// sites reject anything that does not look like one — usually a default CDN
// rule rather than a deliberate policy — and a page the user can read in their
// own browser but the agent cannot is the capability failing at its premise.
//
// It is one fixed string rather than a rotating pool, for two reasons. A pool
// only helps against a blocklist naming one exact string, and no operator
// blocklists mainstream desktop Chrome: doing so would refuse a large share of
// their real visitors. Against that non-threat, rotation costs something
// concrete — a site that did refuse one entry would produce a failure that
// reproduced a fraction of the time, which is the most expensive shape a bug
// can have. A fixed identity fails the same way every time, or not at all.
//
// Desktop rather than mobile is also deliberate: mobile identities are served
// a different and usually smaller document, and these tools exist to read the
// whole article.
//
// This is the entirety of what Collomia does about being refused. Nothing here
// forges TLS fingerprints, solves challenges, rotates addresses, or retries a
// refusal, and a 403 is returned to the model as a 403. A site that has decided
// to refuse automated clients succeeds in refusing this one.
//
// The version number goes stale as Chrome advances; docs/RELEASING.md carries
// refreshing it as a release step.
const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"

// Client retrieves public web content under fixed bounds.
type Client struct {
	http      *http.Client
	maxBytes  int64
	userAgent string
}

// Options configures a Client. The zero value is the intended configuration
// for real use; the fields exist for tests and for the search backends.
type Options struct {
	// Timeout bounds one retrieval. Zero uses DefaultTimeout.
	Timeout time.Duration
	// MaxBytes bounds one response body. Zero uses DefaultMaxBytes.
	MaxBytes int64
	// AllowPrivateAddresses disables the public-internet guard. It exists so
	// this package's own tests can serve fixtures from loopback. Nothing in
	// Collomia sets it, and no configuration key reaches it.
	AllowPrivateAddresses bool
	// UserAgent overrides the identity this client presents. Zero uses
	// UserAgent, which is what ordinary use does; tests set it to observe
	// which identity reached the server.
	UserAgent string
}

// New builds a client. Its transport is not shared with the provider client:
// these requests go to hosts the model chose, and they must not inherit
// connection state, proxies, or credentials from traffic that carries API
// keys.
func New(opts Options) *Client {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	if opts.UserAgent == "" {
		opts.UserAgent = UserAgent
	}
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   dialControl(opts.AllowPrivateAddresses),
	}
	transport := &http.Transport{
		DialContext: dialer.DialContext,
		// HTTP/1.1 only, and this is load-bearing rather than conservative.
		//
		// Go's HTTP/2 client sends a distinctive SETTINGS frame, and bot
		// management products fingerprint it. Measured against Stack Overflow
		// from one machine, one address, and one user agent: over HTTP/2 every
		// request came back 403 with "cf-mitigated: challenge", and over
		// HTTP/1.1 every request came back 200. The header made no difference
		// in either direction; the protocol made all of it.
		//
		// Nothing is being forged here — HTTP/1.1 is a protocol every server
		// speaks, and the fingerprint stops mattering because there is no
		// longer a fingerprint to read. The cost is losing multiplexing, which
		// a tool that fetches one document at a time was never using.
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxIdleConns:          8,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		// Proxies are deliberately not read from the environment. An
		// inherited proxy would route model-chosen requests through a host
		// this package never checked, past the address guard entirely.
		Proxy: nil,
	}
	return &Client{
		http: &http.Client{
			Transport: transport,
			Timeout:   opts.Timeout,
			// No cookie jar: nothing this tool fetches should carry state
			// from anything else it fetched.
			CheckRedirect: checkRedirect,
		},
		maxBytes:  opts.MaxBytes,
		userAgent: opts.UserAgent,
	}
}

// UserAgent is the identity this client presents, for diagnostics.
func (c *Client) UserAgent() string { return c.userAgent }

// Page is one retrieved response.
type Page struct {
	// RequestedURL is what the caller asked for; URL is where the request
	// ended after redirects. They differ only within one site.
	RequestedURL string
	URL          string
	Status       int
	ContentType  string
	Body         []byte
	// Truncated reports that the response exceeded the byte cap.
	Truncated bool
}

// CrossSiteRedirectError reports a redirect that left the requested site.
//
// Following it silently would fetch a host the permission layer never saw:
// web_fetch declares the host in the URL it was given, and an approval for
// that host is not an approval for wherever a redirector points. Returning the
// destination lets the model ask again for a URL the user can actually see.
type CrossSiteRedirectError struct {
	From string
	To   string
}

func (e *CrossSiteRedirectError) Error() string {
	return fmt.Sprintf("%s redirects to a different site: %s. Nothing was fetched. Call web_fetch again with that URL if it is the one you want.", e.From, e.To)
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("refused a redirect to the non-web scheme %q", req.URL.Scheme)
	}
	origin := via[0].URL
	if !sameSite(origin.Hostname(), req.URL.Hostname()) {
		return &CrossSiteRedirectError{From: origin.String(), To: req.URL.String()}
	}
	return nil
}

// sameSite reports whether a redirect stayed within the site that was asked
// for. Equal hosts and the ordinary www/apex and subdomain moves qualify; a
// link shortener pointing somewhere else does not.
func sameSite(from, to string) bool {
	from = strings.ToLower(strings.TrimSuffix(from, "."))
	to = strings.ToLower(strings.TrimSuffix(to, "."))
	if from == to {
		return true
	}
	return strings.HasSuffix(to, "."+from) || strings.HasSuffix(from, "."+to)
}

// ParseTarget validates a model-supplied URL before anything is dialed, so an
// unusable target is a clear message instead of a transport error.
func ParseTarget(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("url could not be parsed: %w", err)
	}
	if parsed.Scheme == "" {
		// A bare "example.com/docs" is what a model writes when it copied a
		// hostname out of prose. Assuming https is safe and saves a round trip
		// through an error message.
		parsed, err = url.Parse("https://" + raw)
		if err != nil {
			return nil, fmt.Errorf("url could not be parsed: %w", err)
		}
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("unsupported url scheme %q: web_fetch reads http and https only", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("url has no host")
	}
	// Credentials in a URL are either a mistake or an attempt to make the
	// tool authenticate somewhere; either way they are not sent.
	parsed.User = nil
	return parsed, nil
}

// Get retrieves one URL.
func (c *Client) Get(ctx context.Context, target *url.URL) (Page, error) {
	return c.do(ctx, http.MethodGet, target, nil, "")
}

// PostForm submits a form-encoded request. The search backends use it; DDG's
// HTML endpoints accept a POSTed query without the token round trip their
// JavaScript interface needs.
func (c *Client) PostForm(ctx context.Context, target *url.URL, form url.Values) (Page, error) {
	return c.do(ctx, http.MethodPost, target, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
}

func (c *Client) do(ctx context.Context, method string, target *url.URL, body io.Reader, contentType string) (Page, error) {
	page := Page{RequestedURL: target.String(), URL: target.String()}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return page, err
	}
	request.Header.Set("User-Agent", c.userAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,application/json;q=0.8,*/*;q=0.5")
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return page, unwrapRequestError(err)
	}
	defer response.Body.Close()
	page.URL = response.Request.URL.String()
	page.Status = response.StatusCode
	page.ContentType = response.Header.Get("Content-Type")

	// Refusing an oversized response by its declared length avoids reading
	// megabytes only to discard them.
	if declared := response.Header.Get("Content-Length"); declared != "" {
		if size, err := strconv.ParseInt(declared, 10, 64); err == nil && size > c.maxBytes {
			return page, fmt.Errorf("response is %d bytes, over the %d byte limit for web content", size, c.maxBytes)
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, c.maxBytes+1))
	if err != nil {
		return page, fmt.Errorf("reading %s: %w", page.URL, err)
	}
	if int64(len(data)) > c.maxBytes {
		data = data[:c.maxBytes]
		page.Truncated = true
	}
	page.Body = data
	return page, nil
}

// unwrapRequestError surfaces the guard's own explanation instead of the
// generic transport wrapper that hides it.
func unwrapRequestError(err error) error {
	var blocked *BlockedAddressError
	if errors.As(err, &blocked) {
		return blocked
	}
	var crossSite *CrossSiteRedirectError
	if errors.As(err, &crossSite) {
		return crossSite
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s %s: %w", urlErr.Op, urlErr.URL, urlErr.Err)
	}
	return err
}
