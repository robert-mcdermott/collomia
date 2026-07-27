package web

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// This file implements web search against DuckDuckGo's HTML endpoints.
//
// DuckDuckGo is the backend because it is the only major engine that answers
// a plain query without an API key, a billing account, or a per-user quota,
// which is what "built in and always available" requires. There is no Go
// client library for it worth depending on; what exists wraps the same two
// endpoints used here.
//
// Both endpoints are HTML meant for people, so this is scraping, and scraping
// breaks. Two things keep that from being a silent failure: the endpoints are
// tried in order so a change to one is survivable, and a page that parses to
// zero results is reported as an engine failure rather than as "no results",
// which is the difference between a user retrying a query and a user
// concluding the web has nothing on it.

// SearchResult is one ranked result.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// SearchResponse is a completed search.
type SearchResponse struct {
	Query   string
	Engine  string
	Results []SearchResult
}

const (
	// DefaultResults is the result count when the caller does not choose.
	DefaultResults = 5
	// MaxResults bounds one search. More than this is a reading list, not an
	// answer, and it crowds the context that the reading has to happen in.
	MaxResults = 15
	// maxSnippetBytes and maxTitleBytes bound attacker-controlled text before
	// it reaches the transcript.
	maxSnippetBytes = 500
	maxTitleBytes   = 200
)

type searchEndpoint struct {
	name string
	url  string
}

// searchEndpoints are tried in order. Both are DuckDuckGo's no-JavaScript
// interfaces and both answer a POSTed query directly.
var searchEndpoints = []searchEndpoint{
	{name: "duckduckgo-html", url: "https://html.duckduckgo.com/html/"},
	{name: "duckduckgo-lite", url: "https://lite.duckduckgo.com/lite/"},
}

// SearchHosts are the endpoints a search may contact, for the permission
// layer to declare before one runs.
func SearchHosts() []string {
	hosts := make([]string, 0, len(searchEndpoints))
	for _, endpoint := range searchEndpoints {
		if parsed, err := url.Parse(endpoint.url); err == nil {
			hosts = append(hosts, parsed.Hostname())
		}
	}
	return hosts
}

// Search runs one query and returns ranked results.
func (c *Client) Search(ctx context.Context, query string, limit int) (SearchResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return SearchResponse{}, errors.New("query is required")
	}
	if limit <= 0 {
		limit = DefaultResults
	}
	if limit > MaxResults {
		limit = MaxResults
	}
	var failures []string
	for _, endpoint := range searchEndpoints {
		target, err := url.Parse(endpoint.url)
		if err != nil {
			continue
		}
		page, err := c.PostForm(ctx, target, url.Values{"q": {query}, "kl": {"wt-wt"}})
		if err != nil {
			var blocked *BlockedAddressError
			if errors.As(err, &blocked) {
				return SearchResponse{}, err
			}
			failures = append(failures, endpoint.name+": "+err.Error())
			continue
		}
		if reason := searchRefusal(page); reason != "" {
			failures = append(failures, endpoint.name+": "+reason)
			continue
		}
		results, err := parseResults(page.Body, limit)
		if err != nil {
			failures = append(failures, endpoint.name+": "+err.Error())
			continue
		}
		if len(results) == 0 {
			failures = append(failures, endpoint.name+": the response contained no recognizable results")
			continue
		}
		return SearchResponse{Query: query, Engine: endpoint.name, Results: results}, nil
	}
	if ctx.Err() != nil {
		return SearchResponse{}, ctx.Err()
	}
	return SearchResponse{}, fmt.Errorf("web search failed on every endpoint (%s)", strings.Join(failures, "; "))
}

// searchRefusal explains a non-answer, or "" when the response is a result
// page to parse.
//
// DuckDuckGo answers a throttled client with 202 and an anti-bot challenge
// page rather than the 429 the situation calls for. Reported verbatim that
// becomes "HTTP 202", which reads like a bug in Collomia and sends the user to
// look for one. It is worth naming, because the fix is to wait rather than to
// change anything: the limit is per address, it lifts on its own in a few
// minutes, and it is reached by a burst of searches, not by a session's worth.
func searchRefusal(page Page) string {
	switch {
	case page.Status == 200:
		return ""
	case page.Status == 202 || page.Status == 429:
		return fmt.Sprintf("rate limited (HTTP %d) — DuckDuckGo throttles bursts of searches per address and lifts it after a few minutes; wait rather than retrying immediately", page.Status)
	case page.Status == 403:
		return "refused the request (HTTP 403)"
	default:
		return fmt.Sprintf("HTTP %d", page.Status)
	}
}

// resultLinkClasses and snippetClasses are the markers each endpoint uses.
// Matching on class rather than on document position is what lets one parser
// read both layouts, and lets an unrelated markup change pass harmlessly.
var resultLinkClasses = []string{"result__a", "result-link"}
var snippetClasses = []string{"result__snippet", "result-snippet"}

// adMarkers appear on the container of a sponsored result.
var adMarkers = []string{"result--ad", "results--ad", "result--sponsored"}

func parseResults(body []byte, limit int) ([]SearchResult, error) {
	root, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("the response could not be parsed as HTML: %w", err)
	}
	// Flattening to document order lets a snippet be found by scanning
	// forward from its link, which is the one thing both layouts share: the
	// HTML endpoint nests the snippet beside the link, the lite endpoint puts
	// it in the next table row.
	var nodes []*html.Node
	var flatten func(*html.Node)
	flatten = func(node *html.Node) {
		nodes = append(nodes, node)
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			flatten(child)
		}
	}
	flatten(root)

	var results []SearchResult
	seen := map[string]bool{}
	for i, node := range nodes {
		if !isResultLink(node) {
			continue
		}
		target := unwrapRedirect(attr(node, "href"))
		if target == "" || seen[target] {
			continue
		}
		title := bound(textOf(node), maxTitleBytes)
		if title == "" {
			continue
		}
		seen[target] = true
		results = append(results, SearchResult{
			Title:   title,
			URL:     target,
			Snippet: bound(snippetAfter(nodes, i), maxSnippetBytes),
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func isResultLink(node *html.Node) bool {
	if node.Type != html.ElementNode || node.DataAtom != atom.A {
		return false
	}
	if !hasAnyClass(node, resultLinkClasses) {
		return false
	}
	for ancestor := node.Parent; ancestor != nil; ancestor = ancestor.Parent {
		if hasAnyClass(ancestor, adMarkers) {
			return false
		}
	}
	return true
}

// snippetAfter finds the description belonging to the link at index, by
// scanning forward until the next result link.
func snippetAfter(nodes []*html.Node, index int) string {
	for i := index + 1; i < len(nodes); i++ {
		node := nodes[i]
		if isResultLink(node) {
			return ""
		}
		if node.Type == html.ElementNode && hasAnyClass(node, snippetClasses) {
			return textOf(node)
		}
	}
	return ""
}

func hasAnyClass(node *html.Node, wanted []string) bool {
	if node.Type != html.ElementNode {
		return false
	}
	classes := strings.Fields(attr(node, "class"))
	for _, class := range classes {
		for _, want := range wanted {
			if class == want {
				return true
			}
		}
	}
	return false
}

// unwrapRedirect recovers the destination from DuckDuckGo's click-tracking
// wrapper and refuses anything that is not a public web URL, so a result list
// cannot hand the model a javascript: or file: target to fetch.
func unwrapRedirect(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if inner := parsed.Query().Get("uddg"); inner != "" {
		if decoded, err := url.Parse(inner); err == nil {
			parsed = decoded
		}
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	// Ad and internal links point back at the engine; they are not results.
	if host == "" || host == "duckduckgo.com" || strings.HasSuffix(host, ".duckduckgo.com") || host == "duck.co" {
		return ""
	}
	return parsed.String()
}

func bound(value string, limit int) string {
	value = collapse(value)
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && (value[end]&0xC0) == 0x80 {
		end--
	}
	return strings.TrimSpace(value[:end]) + "…"
}
