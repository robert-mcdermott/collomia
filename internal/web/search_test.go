package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// htmlLayout mirrors html.duckduckgo.com: the title link and the snippet are
// siblings inside one result container, and the snippet is itself an anchor.
const htmlLayout = `<!doctype html><html><body>
<div class="results">
  <div class="result results_links result--ad">
    <div class="links_main result__body">
      <h2 class="result__title"><a rel="nofollow" class="result__a" href="https://sponsor.test/buy">Buy Go Support</a></h2>
      <a class="result__snippet" href="https://sponsor.test/buy">Sponsored offer</a>
    </div>
  </div>
  <div class="result results_links web-result">
    <div class="links_main result__body">
      <h2 class="result__title"><a rel="nofollow" class="result__a" href="https://pkg.go.dev/context">context package - Go Packages</a></h2>
      <div class="result__extras"><a class="result__url" href="https://pkg.go.dev/context">pkg.go.dev/context</a></div>
      <a class="result__snippet" href="https://pkg.go.dev/context"><b>Package</b> context defines the Context type, which carries deadlines &amp; cancellation signals.</a>
    </div>
  </div>
  <div class="result results_links web-result">
    <div class="links_main result__body">
      <h2 class="result__title"><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fblog%2Fcontext&amp;rut=abc">Go Concurrency Patterns: Context</a></h2>
      <a class="result__snippet" href="https://go.dev/blog/context">In Go servers, each incoming request is handled in its own goroutine.</a>
    </div>
  </div>
  <div class="result results_links web-result">
    <div class="links_main result__body">
      <h2 class="result__title"><a rel="nofollow" class="result__a" href="https://pkg.go.dev/context">context package - Go Packages</a></h2>
      <a class="result__snippet" href="https://pkg.go.dev/context">A duplicate of the first result.</a>
    </div>
  </div>
</div></body></html>`

// liteLayout mirrors lite.duckduckgo.com: a table where the snippet lives in
// the row after the link, not beside it.
const liteLayout = `<!doctype html><html><body><table border="0">
  <tr><td valign="top">1.&nbsp;</td><td><a rel="nofollow" href="https://pkg.go.dev/context" class='result-link'>context package - Go Packages</a></td></tr>
  <tr><td>&nbsp;</td><td class='result-snippet'><b>Package</b> context defines the Context type.</td></tr>
  <tr><td>&nbsp;</td><td><span class='link-text'>pkg.go.dev/context</span></td></tr>
  <tr><td valign="top">2.&nbsp;</td><td><a rel="nofollow" href="https://go.dev/blog/context" class='result-link'>Go Concurrency Patterns: Context</a></td></tr>
  <tr><td>&nbsp;</td><td class='result-snippet'>Each incoming request is handled in its own goroutine.</td></tr>
</table></body></html>`

func TestParseResultsReadsBothDuckDuckGoLayouts(t *testing.T) {
	for name, fixture := range map[string]string{"html": htmlLayout, "lite": liteLayout} {
		results, err := parseResults([]byte(fixture), 10)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(results) != 2 {
			t.Fatalf("%s: got %d results, want 2: %+v", name, len(results), results)
		}
		if results[0].URL != "https://pkg.go.dev/context" || !strings.Contains(results[0].Title, "context package") {
			t.Errorf("%s: first result = %+v", name, results[0])
		}
		if !strings.Contains(results[0].Snippet, "defines the Context type") {
			t.Errorf("%s: snippet was not paired with its link: %+v", name, results[0])
		}
		if results[1].URL != "https://go.dev/blog/context" {
			t.Errorf("%s: second result = %+v", name, results[1])
		}
	}
}

func TestParseResultsDropsAdsUnwrapsRedirectsAndDeduplicates(t *testing.T) {
	results, err := parseResults([]byte(htmlLayout), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if strings.Contains(result.URL, "sponsor.test") {
			t.Errorf("a sponsored result was returned: %+v", result)
		}
		if strings.Contains(result.URL, "duckduckgo.com") {
			t.Errorf("a click-tracking wrapper was not unwrapped: %+v", result)
		}
	}
	if len(results) != 2 {
		t.Fatalf("the duplicate URL was not collapsed: %+v", results)
	}
}

func TestParseResultsHonorsTheLimit(t *testing.T) {
	results, err := parseResults([]byte(htmlLayout), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

func TestUnwrapRedirectRefusesNonWebTargets(t *testing.T) {
	for _, href := range []string{
		"javascript:alert(1)", "data:text/html,x", "file:///etc/passwd", "",
		"https://duckduckgo.com/y.js?ad_provider=x", "//duckduckgo.com/l/?uddg=javascript%3Aalert(1)",
	} {
		if got := unwrapRedirect(href); got != "" {
			t.Errorf("unwrapRedirect(%q) = %q; a search result must never hand back a non-web target", href, got)
		}
	}
	if got := unwrapRedirect("//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc"); got != "https://go.dev/doc" {
		t.Errorf("wrapped result URL = %q", got)
	}
}

func TestSearchFailsOverToTheNextEndpointAndReportsTotalFailure(t *testing.T) {
	// DuckDuckGo answers a throttled client with 202 and a challenge page, not
	// with the 429 the situation calls for, so both must read as throttling.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("<html><body>challenge</body></html>"))
	}))
	defer broken.Close()
	// A 200 that parses to nothing is the shape a layout change takes. It must
	// fail over rather than be reported as "the web has no results".
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body><p>no results here</p></body></html>"))
	}))
	defer empty.Close()
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(liteLayout))
	}))
	defer working.Close()

	restore := searchEndpoints
	defer func() { searchEndpoints = restore }()

	searchEndpoints = []searchEndpoint{
		{name: "broken", url: broken.URL},
		{name: "empty", url: empty.URL},
		{name: "working", url: working.URL},
	}
	response, err := local().Search(t.Context(), "context package", 5)
	if err != nil {
		t.Fatalf("search did not fail over: %v", err)
	}
	if response.Engine != "working" || len(response.Results) != 2 {
		t.Fatalf("response = %+v", response)
	}

	searchEndpoints = []searchEndpoint{{name: "broken", url: broken.URL}, {name: "empty", url: empty.URL}}
	_, err = local().Search(t.Context(), "context package", 5)
	if err == nil {
		t.Fatal("a total failure must be an error, not an empty result set")
	}
	for _, want := range []string{"broken", "empty", "rate limited", "wait rather than retrying"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("failure message should name what happened (%q): %v", want, err)
		}
	}
}

func TestSearchRefusalExplainsThrottlingRatherThanEchoingAStatus(t *testing.T) {
	for _, status := range []int{202, 429} {
		reason := searchRefusal(Page{Status: status})
		if !strings.Contains(reason, "rate limited") || !strings.Contains(reason, "few minutes") {
			t.Errorf("HTTP %d should read as throttling, got %q", status, reason)
		}
	}
	if searchRefusal(Page{Status: 200}) != "" {
		t.Error("a 200 is a result page, not a refusal")
	}
	if reason := searchRefusal(Page{Status: 503}); !strings.Contains(reason, "503") {
		t.Errorf("an unclassified status should still be reported: %q", reason)
	}
}

func TestSearchRejectsAnEmptyQuery(t *testing.T) {
	if _, err := local().Search(t.Context(), "   ", 5); err == nil {
		t.Fatal("an empty query must be rejected before a request is made")
	}
}

func TestSearchHostsCoverEveryEndpointTheSearchMayContact(t *testing.T) {
	hosts := SearchHosts()
	if len(hosts) != len(searchEndpoints) {
		t.Fatalf("declared hosts %v do not cover every endpoint %v", hosts, searchEndpoints)
	}
	for _, host := range hosts {
		if !strings.HasSuffix(host, "duckduckgo.com") {
			t.Errorf("unexpected declared host %q", host)
		}
	}
}

func TestSnippetsAndTitlesAreBounded(t *testing.T) {
	long := strings.Repeat("attacker controlled text ", 200)
	fixture := `<html><body><a class="result-link" href="https://example.com/x">` + long + `</a><div class="result-snippet">` + long + `</div></body></html>`
	results, err := parseResults([]byte(fixture), 5)
	if err != nil || len(results) != 1 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	if len(results[0].Title) > maxTitleBytes+8 || len(results[0].Snippet) > maxSnippetBytes+8 {
		t.Fatalf("unbounded result text: title=%d snippet=%d", len(results[0].Title), len(results[0].Snippet))
	}
}
