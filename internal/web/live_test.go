package web

import (
	"os"
	"strings"
	"testing"
)

// The ordinary suite tests this package against fixtures and loopback servers,
// so it stays credential-free, offline, and safe for pull requests. This file
// is the opt-in counterpart: it checks the two things fixtures cannot, namely
// that DuckDuckGo still answers the request Collomia actually sends, and that
// its result markup still parses. Those are the failures that would otherwise
// reach a user as "the web has nothing on that".
//
//	COLLO_LIVE_WEB_TESTS=1 go test ./internal/web -run Live -v
//
// Run it sparingly. DuckDuckGo rate limits by address and answers a throttled
// client with HTTP 202 and a challenge page, so a tight loop of runs will
// start reporting failures that say nothing about this code. The limit lifts
// on its own after a few minutes.
const liveWebTestsEnv = "COLLO_LIVE_WEB_TESTS"

func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv(liveWebTestsEnv) != "1" {
		t.Skip("set COLLO_LIVE_WEB_TESTS=1 to run live web tests against real endpoints")
	}
}

func TestLiveSearchReturnsUsableResultsFromEveryEndpoint(t *testing.T) {
	requireLive(t)
	client := New(Options{})
	for _, endpoint := range searchEndpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			restore := searchEndpoints
			defer func() { searchEndpoints = restore }()
			// Each endpoint is exercised alone, so a working fallback cannot
			// hide a primary that has stopped parsing.
			searchEndpoints = []searchEndpoint{endpoint}

			response, err := client.Search(t.Context(), "golang context package cancellation", 5)
			if err != nil {
				t.Fatalf("%s: %v", endpoint.name, err)
			}
			if len(response.Results) < 3 {
				t.Fatalf("%s returned %d results: %+v", endpoint.name, len(response.Results), response.Results)
			}
			withSnippets := 0
			for _, result := range response.Results {
				if !strings.HasPrefix(result.URL, "http") || result.Title == "" {
					t.Errorf("%s: malformed result %+v", endpoint.name, result)
				}
				if result.Snippet != "" {
					withSnippets++
				}
			}
			if withSnippets == 0 {
				t.Errorf("%s: no result carried a snippet; the snippet markup may have changed", endpoint.name)
			}
			t.Logf("%s: %d results, first = %s (%s)", endpoint.name, len(response.Results), response.Results[0].Title, response.Results[0].URL)
		})
	}
}

func TestLiveFetchExtractsReadableTextFromARealPage(t *testing.T) {
	requireLive(t)
	client := New(Options{})
	target, err := ParseTarget("https://pkg.go.dev/context")
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Get(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	if page.Status != 200 {
		t.Fatalf("HTTP %d", page.Status)
	}
	text, err := Extract(page, FormatText)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Context") || len(text) < 500 {
		t.Fatalf("extraction produced %d bytes:\n%s", len(text), text)
	}
	if strings.Contains(text, "<div") || strings.Contains(text, "function(") {
		t.Errorf("markup or script survived extraction:\n%s", text[:min(len(text), 2000)])
	}
}

func TestLiveGuardRefusesCloudMetadataForReal(t *testing.T) {
	requireLive(t)
	target, err := ParseTarget("http://169.254.169.254/latest/meta-data/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{}).Get(t.Context(), target); err == nil {
		t.Fatal("the instance metadata service was reachable")
	} else {
		t.Logf("refused as expected: %v", err)
	}
}
