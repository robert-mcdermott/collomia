package web

import (
	"strings"
	"testing"
)

func page(contentType, body string) Page {
	return Page{URL: "https://example.com/doc", RequestedURL: "https://example.com/doc", Status: 200, ContentType: contentType, Body: []byte(body)}
}

func extract(t *testing.T, p Page, format Format) string {
	t.Helper()
	out, err := Extract(p, format)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return out
}

const article = `<!doctype html>
<html><head>
  <title>Rate limiting in v3</title>
  <style>.hidden{display:none}</style>
  <script>window.analytics={track(){}}</script>
</head><body>
  <nav><a href="/">Home</a><a href="/pricing">Pricing</a></nav>
  <header><h1>Acme Docs</h1></header>
  <div role="banner">Cookie consent: we value your privacy</div>
  <main>
    <h1>Rate limiting</h1>
    <p>Requests are limited to 100 per minute &amp; burst to 200.</p>
    <h2>Handling 429</h2>
    <p>Read the <a href="/docs/retry">Retry-After</a> header and wait.</p>
    <ul><li>Back off exponentially</li><li>Respect <code>Retry-After</code></li></ul>
    <pre><code>resp = client.get(url)
if resp.status == 429:
    sleep(retry_after)</code></pre>
    <table>
      <tr><th>Plan</th><th>Limit</th></tr>
      <tr><td>Free</td><td>100/min</td></tr>
      <tr><td>Pro</td><td>1000/min</td></tr>
    </table>
    <p>This paragraph has enough words to make the main element the clear content root for the extractor, because a short main element is more likely to be a layout wrapper than the article body itself.</p>
  </main>
  <aside><p>Related: billing, quotas, webhooks</p></aside>
  <footer>&copy; Acme, all rights reserved</footer>
</body></html>`

func TestExtractKeepsContentAndDropsChrome(t *testing.T) {
	text := extract(t, page("text/html; charset=utf-8", article), FormatText)

	for _, want := range []string{
		"# Rate limiting in v3",
		"# Rate limiting",
		"## Handling 429",
		"limited to 100 per minute & burst to 200",
		"- Back off exponentially",
		"```",
		"if resp.status == 429:",
		"| Plan | Limit |",
		"| Pro | 1000/min |",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("extracted text is missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{
		"window.analytics", "display:none", "Pricing", "Cookie consent",
		"all rights reserved", "Related: billing",
	} {
		if strings.Contains(text, unwanted) {
			t.Errorf("extracted text still contains chrome %q:\n%s", unwanted, text)
		}
	}
}

func TestExtractTextDropsLinkTargetsAndMarkdownKeepsThem(t *testing.T) {
	text := extract(t, page("text/html", article), FormatText)
	if strings.Contains(text, "https://example.com/docs/retry") {
		t.Errorf("format=text should not carry link targets:\n%s", text)
	}
	if !strings.Contains(text, "Retry-After") {
		t.Error("link text must survive in format=text")
	}

	markdown := extract(t, page("text/html", article), FormatMarkdown)
	if !strings.Contains(markdown, "[Retry-After](https://example.com/docs/retry)") {
		t.Errorf("format=markdown must resolve relative links against the page URL:\n%s", markdown)
	}
}

func TestExtractFallsBackToBodyWhenMainIsJustAWrapper(t *testing.T) {
	body := `<html><body><main><span>Menu</span></main><div><p>The whole article lives in a plain div, which is still the common case on the web, so the extractor must not return a three-word navigation wrapper just because it was tagged main.</p></div></body></html>`
	text := extract(t, page("text/html", body), FormatText)
	if !strings.Contains(text, "The whole article lives in a plain div") {
		t.Fatalf("a short main element must not win over the body:\n%s", text)
	}
}

func TestExtractDispatchesOnContentType(t *testing.T) {
	json := `{"version":"3.1.0","deprecated":false}`
	if out := extract(t, page("application/json", json), FormatText); out != json {
		t.Errorf("JSON must pass through unchanged, got %q", out)
	}
	if out := extract(t, page("text/plain", "plain body"), FormatText); out != "plain body" {
		t.Errorf("text/plain must pass through, got %q", out)
	}
	// A server that declares nothing still gets classified.
	if out := extract(t, page("", "<html><body><p>sniffed</p></body></html>"), FormatText); !strings.Contains(out, "sniffed") {
		t.Errorf("undeclared HTML should be sniffed, got %q", out)
	}
}

func TestExtractRefusesBinaryWithItsTypeAndSize(t *testing.T) {
	binary := page("image/png", "\x89PNG\r\n\x1a\n\x00\x00\x00fixture")
	for _, format := range []Format{FormatText, FormatMarkdown, FormatRaw} {
		_, err := Extract(binary, format)
		if err == nil {
			t.Fatalf("format=%s inlined binary content", format)
		}
		if !strings.Contains(err.Error(), "image/png") || !strings.Contains(err.Error(), "not text") {
			t.Errorf("refusal should name the type: %v", err)
		}
	}
}

func TestExtractRawReturnsTheBodyUnchanged(t *testing.T) {
	source := "<html><body><script>keep me</script><p>and me</p></body></html>"
	if out := extract(t, page("text/html", source), FormatRaw); out != source {
		t.Fatalf("format=raw altered the body:\n%s", out)
	}
}

func TestParseFormatRejectsUnknownValues(t *testing.T) {
	if got, err := ParseFormat(""); err != nil || got != FormatText {
		t.Errorf("empty format should default to text, got %q %v", got, err)
	}
	if got, err := ParseFormat("MARKDOWN"); err != nil || got != FormatMarkdown {
		t.Errorf("format should be case-insensitive, got %q %v", got, err)
	}
	if _, err := ParseFormat("pdf"); err == nil {
		t.Error("an unknown format must be an error the model can read")
	}
}

func TestExtractCollapsesWhitespaceWithoutGluingWordsTogether(t *testing.T) {
	body := `<html><body><div><p>first</p></div>


	<div><p>second</p></div><p>a <b>bold</b> word</p></body></html>`
	text := extract(t, page("text/html", body), FormatText)
	if strings.Contains(text, "\n\n\n") {
		t.Errorf("blank runs were not collapsed:\n%q", text)
	}
	if !strings.Contains(text, "a bold word") {
		t.Errorf("inline elements must not lose their spacing:\n%q", text)
	}
}

func TestExtractBoundsPathologicalPages(t *testing.T) {
	huge := "<html><body>" + strings.Repeat("<p>filler filler filler</p>", 200000) + "</body></html>"
	out := extract(t, page("text/html", huge), FormatText)
	if len(out) > maxExtractedBytes+64 {
		t.Fatalf("extraction was not bounded: %d bytes", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Error("a truncated result must say so")
	}
}
