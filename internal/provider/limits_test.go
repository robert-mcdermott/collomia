package provider

import (
	"strings"
	"testing"
)

func TestNormalizeModelIDCollapsesEverySpelling(t *testing.T) {
	// The same weights reach Collomia under four different names depending on
	// how they are served. A table keyed on the raw string would need one entry
	// per spelling, and the spelling nobody thought of is the one that ends up
	// with a guessed context window.
	for _, tc := range []struct{ input, want string }{
		{"claude-sonnet-4-5-20250929", "claude-sonnet-4-5-20250929"},
		{"us.anthropic.claude-sonnet-4-5-20250929-v1:0", "claude-sonnet-4-5-20250929-v1"},
		{"eu.anthropic.claude-opus-4-1", "claude-opus-4-1"},
		{"anthropic/claude-sonnet-4.5", "claude-sonnet-4.5"},
		{"Claude-Sonnet-4", "claude-sonnet-4"},
		{"glm-5.2:cloud", "glm-5.2"},
		{"qwen/qwen3-coder", "qwen3-coder"},
		{"  gpt-4o  ", "gpt-4o"},
		{"", ""},
	} {
		if got := normalizeModelID(tc.input); got != tc.want {
			t.Errorf("normalizeModelID(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNormalizeModelIDKeepsVersionNumbers(t *testing.T) {
	// The vendor-segment strip is dot-separated, and so are version numbers.
	// Treating any leading segment as a vendor would turn gpt-4.1 into "1".
	for _, id := range []string{"gpt-4.1", "qwen2.5-coder", "gemma3", "llama3.1"} {
		if got := normalizeModelID(id); got != id {
			t.Errorf("normalizeModelID(%q) = %q; a version number is not a vendor segment", id, got)
		}
	}
}

func TestKnownLimitsPrefersTheMostSpecificEntry(t *testing.T) {
	// The family floors exist for models released after this build, and must
	// never answer for a model that has its own entry.
	specific, ok := KnownLimits("claude-3-5-sonnet-20241022")
	if !ok {
		t.Fatal("a documented model must be recognized")
	}
	family, ok := KnownLimits("claude-quintuple-9")
	if !ok {
		t.Fatal("an unrecognized model in a known family must fall to the family floor")
	}
	if specific.MaxOutput == family.MaxOutput {
		t.Errorf("the family floor (%d) answered for a model with its own entry (%d)", family.MaxOutput, specific.MaxOutput)
	}
}

func TestKnownLimitsNeverReturnsAnUnsatisfiableCombination(t *testing.T) {
	// Every entry is written into a configuration where max_tokens is spent
	// out of the same budget as the prompt. An entry whose output cap met or
	// exceeded its window would produce a provider block that no request can
	// satisfy — and it would be written by the wizard, silently.
	for _, entry := range limitEntries {
		if entry.context <= 0 || entry.output <= 0 {
			t.Errorf("%s: both limits must be positive, got context %d output %d", entry.prefix, entry.context, entry.output)
		}
		if entry.output >= entry.context {
			t.Errorf("%s: output cap %d is at or above the context window %d", entry.prefix, entry.output, entry.context)
		}
	}
}

func TestKnownLimitsHasNoShadowedEntry(t *testing.T) {
	// Matching is longest-prefix-first, so an entry can only be unreachable by
	// being a duplicate. A silently unreachable entry is a maintenance trap:
	// someone adds the numbers, the table appears to know the model, and the
	// floor answers instead.
	seen := map[string]bool{}
	for _, entry := range limitEntries {
		if seen[entry.prefix] {
			t.Errorf("duplicate prefix %q; the second entry can never be reached", entry.prefix)
		}
		seen[entry.prefix] = true
	}
}

func TestResolveLimitsPrefersTheEndpointOverTheTable(t *testing.T) {
	// The table is vendor documentation that cannot be verified from inside a
	// build; the endpoint is stating a fact about itself. A local runtime that
	// has loaded a model with a smaller window is serving the smaller one, and
	// writing the documented number down would disable compaction exactly where
	// it is needed soonest.
	resolved := ResolveLimits("qwen3-coder", Limits{ContextWindow: 8192, ContextSource: LimitsEndpoint, OutputSource: LimitsEndpoint})
	if resolved.ContextWindow != 8192 {
		t.Errorf("the endpoint's own window must win, got %d", resolved.ContextWindow)
	}
	if resolved.ContextSource != LimitsEndpoint {
		t.Errorf("source must stay endpoint, got %q", resolved.ContextSource)
	}
	if resolved.MaxOutput <= 0 {
		t.Error("a catalog that publishes a window but no output cap must still get one from the table")
	}
}

func TestResolveLimitsFallsBackToTheTableThenToNothing(t *testing.T) {
	table := ResolveLimits("claude-3-5-sonnet-20241022", Limits{})
	if table.ContextSource != LimitsTable || table.ContextWindow <= 0 {
		t.Errorf("a documented model with no endpoint report must come from the table, got %+v", table)
	}
	unknown := ResolveLimits("something-nobody-has-heard-of", Limits{})
	if unknown.Known() {
		t.Errorf("an unrecognized model must resolve to nothing rather than a guess, got %+v", unknown)
	}
}

func TestLimitsDescribeNamesItsSource(t *testing.T) {
	// A number presented without its source reads as a measurement. The
	// distinction is the whole reason the table is allowed to be approximate.
	endpoint := Limits{ContextWindow: 200000, MaxOutput: 32000, ContextSource: LimitsEndpoint, OutputSource: LimitsEndpoint}.Describe()
	if !strings.Contains(endpoint, "endpoint") {
		t.Errorf("an endpoint-reported limit must say so: %q", endpoint)
	}
	table := Limits{ContextWindow: 200000, ContextSource: LimitsTable, OutputSource: LimitsTable}.Describe()
	if !strings.Contains(table, "not measured") {
		t.Errorf("a table limit must not read as a measurement: %q", table)
	}
	if got := (Limits{}).Describe(); got != "limits unknown" {
		t.Errorf("nothing established must say so, got %q", got)
	}
}

func TestLimitsDescribeSourcesEachNumberSeparately(t *testing.T) {
	// The commonest real case there is, and the one a single shared source got
	// wrong: a local runtime states the window it is serving and says nothing
	// about an output cap, so the cap is filled in. Reporting the pair as
	// "reported by the endpoint" presents an assumption as a measurement, which
	// is the one thing this whole mechanism exists to stop.
	mixed := Limits{
		ContextWindow: 8192, ContextSource: LimitsEndpoint,
		MaxOutput: 4096, OutputSource: LimitsAssumed,
	}.Describe()
	if !strings.Contains(mixed, "context 8192 (reported by the endpoint)") {
		t.Errorf("the measured number must be credited to the endpoint: %q", mixed)
	}
	if !strings.Contains(mixed, "output 4096 (assumed)") {
		t.Errorf("the filled-in number must not inherit the other's provenance: %q", mixed)
	}
}

func TestResolveLimitsSourcesEachHalfIndependently(t *testing.T) {
	// A catalog stating only a window must produce an endpoint-sourced window
	// beside a table-sourced cap, not one source covering both.
	resolved := ResolveLimits("claude-3-5-sonnet-20241022", Limits{ContextWindow: 120000, ContextSource: LimitsEndpoint})
	if resolved.ContextSource != LimitsEndpoint {
		t.Errorf("context source = %q", resolved.ContextSource)
	}
	if resolved.OutputSource != LimitsTable {
		t.Errorf("output source = %q, want the table that actually supplied it", resolved.OutputSource)
	}
}
