package provider

import (
	"sort"
	"strings"
)

// LimitSource records where a model's limits came from, because the answer
// governs what may be done with them.
//
// The distinction is the whole design of this file. An endpoint that publishes
// its own numbers is stating a fact about itself and may be used to contradict
// a configured value; the table below is a floor assembled from vendor
// documentation, which goes stale by construction and may only ever supply a
// value where none exists.
type LimitSource string

const (
	// LimitsUnknown means nothing established a limit.
	LimitsUnknown LimitSource = ""
	// LimitsEndpoint is what the endpoint's own catalog reported. Authoritative.
	LimitsEndpoint LimitSource = "endpoint"
	// LimitsTable is this package's published-limits table: a deliberate
	// understatement, never a measurement.
	LimitsTable LimitSource = "table"
	// LimitsConfigured is a value the user wrote down. It establishes nothing
	// about the model, but it is theirs, so nothing here overrides it.
	LimitsConfigured LimitSource = "configured"
	// LimitsAssumed is the last-resort default, established by nothing.
	LimitsAssumed LimitSource = "assumed"
)

// Limits describes what one model can hold and what it can emit.
//
// Both halves matter and they fail differently. A missing context window makes
// Agent.shouldCompact return false for the life of a session, so automatic
// compaction never runs and the session ends at a provider context-length
// error. A missing output cap is normalized to a small default, which truncates
// long answers with no message.
type Limits struct {
	// ContextWindow is the total prompt-plus-completion budget.
	ContextWindow int
	// MaxOutput is the largest completion the model will produce.
	MaxOutput int
	// ContextSource and OutputSource say how much each number is worth,
	// separately.
	//
	// One shared source would be a lie in the commonest case there is: a
	// runtime that states the window it is serving and says nothing about an
	// output cap. That resolves to a measured window beside a filled-in cap,
	// and reporting the pair as "reported by the endpoint" presents an
	// assumption as a measurement — the exact failure this whole file exists
	// to remove.
	ContextSource LimitSource
	OutputSource  LimitSource
}

// Known reports whether anything at all was established.
func (l Limits) Known() bool { return l.ContextWindow > 0 || l.MaxOutput > 0 }

// Authoritative reports whether the context window came from the endpoint
// itself, which is the only source permitted to contradict a configured value.
func (l Limits) Authoritative() bool { return l.ContextSource == LimitsEndpoint }

// Describe renders the limits for a status line, naming each number's source
// rather than presenting a table lookup as something that was measured.
func (l Limits) Describe() string {
	parts := make([]string, 0, 2)
	if l.ContextWindow > 0 {
		parts = append(parts, "context "+compactNumber(l.ContextWindow)+" ("+sourceWords(l.ContextSource)+")")
	}
	if l.MaxOutput > 0 {
		parts = append(parts, "output "+compactNumber(l.MaxOutput)+" ("+sourceWords(l.OutputSource)+")")
	}
	if len(parts) == 0 {
		return "limits unknown"
	}
	return strings.Join(parts, " · ")
}

func sourceWords(source LimitSource) string {
	switch source {
	case LimitsEndpoint:
		return "reported by the endpoint"
	case LimitsTable:
		return "published limits; not measured"
	case LimitsConfigured:
		return "as configured"
	default:
		return "assumed"
	}
}

// ResolveLimits picks the best-known limits for a model.
//
// Order is fixed: what the endpoint reported about itself, then the table, then
// nothing. A caller that has no reported limits passes the zero value.
func ResolveLimits(model string, reported Limits) Limits {
	table, hasTable := KnownLimits(model)
	resolved := Limits{}
	// A catalog that publishes a window but no output cap is the common case,
	// so the two halves are filled independently and each keeps its own source.
	if reported.ContextWindow > 0 {
		resolved.ContextWindow, resolved.ContextSource = reported.ContextWindow, LimitsEndpoint
	} else if hasTable && table.ContextWindow > 0 {
		resolved.ContextWindow, resolved.ContextSource = table.ContextWindow, LimitsTable
	}
	if reported.MaxOutput > 0 {
		resolved.MaxOutput, resolved.OutputSource = reported.MaxOutput, LimitsEndpoint
	} else if hasTable && table.MaxOutput > 0 {
		resolved.MaxOutput, resolved.OutputSource = table.MaxOutput, LimitsTable
	}
	return resolved
}

// KnownLimits returns published limits for a model identifier.
//
// **Every entry deliberately understates.** Understating a context window costs
// an earlier compaction and understating an output cap costs a shorter answer;
// overstating either produces a hard provider rejection or a session that never
// compacts and dies at a context-length error. Since this table cannot be
// verified from inside a build and goes stale as vendors ship, it is written to
// fail in the direction that degrades rather than the direction that breaks —
// and it is consulted only where nothing better exists. An endpoint that
// publishes its own numbers always wins, and a value the user configured is
// never overridden by anything here.
//
// The family floors at the end of each group exist for models released after
// this build. A new Claude matching no specific entry should inherit a modern
// Claude's shape rather than a 2023 one, which is the difference between a
// usable default and the 32768-for-everything guess this replaces.
func KnownLimits(model string) (Limits, bool) {
	normalized := normalizeModelID(model)
	if normalized == "" {
		return Limits{}, false
	}
	for _, entry := range limitTable() {
		if strings.HasPrefix(normalized, entry.prefix) {
			return Limits{
				ContextWindow: entry.context, ContextSource: LimitsTable,
				MaxOutput: entry.output, OutputSource: LimitsTable,
			}, true
		}
	}
	return Limits{}, false
}

type limitEntry struct {
	prefix  string
	context int
	output  int
}

var limitEntries = []limitEntry{
	// Anthropic. Reached directly, through Bedrock inference profiles, through
	// Azure Foundry deployments, and through OpenRouter, which is why matching
	// happens on the normalized identifier rather than on the provider type.
	{"claude-3-haiku", 200000, 4096},
	{"claude-3-opus", 200000, 4096},
	{"claude-3-sonnet", 200000, 4096},
	{"claude-3-5-haiku", 200000, 8192},
	{"claude-3-5-sonnet", 200000, 8192},
	{"claude-3-7-sonnet", 200000, 32000},
	{"claude-haiku-4", 200000, 32000},
	{"claude-sonnet-4", 200000, 32000},
	{"claude-opus-4", 200000, 32000},
	{"claude-", 200000, 32000},

	// OpenAI, and the Azure deployments that serve the same models.
	{"gpt-3.5-turbo", 16385, 4096},
	{"gpt-4-turbo", 128000, 4096},
	{"gpt-4o-mini", 128000, 16384},
	{"gpt-4o", 128000, 16384},
	{"gpt-4.1", 1000000, 32768},
	{"gpt-4", 8192, 4096},
	{"gpt-5", 256000, 32768},
	{"gpt-", 128000, 16384},
	{"o1-mini", 128000, 32768},
	{"o1", 200000, 32768},
	{"o3-mini", 200000, 32768},
	{"o3", 200000, 32768},
	{"o4-mini", 200000, 32768},

	// Open-weight families, reached through Ollama, LM Studio, vLLM, Bedrock,
	// and OpenRouter alike. These are the ones whose published context is a
	// property of the weights; what a local runtime will actually serve depends
	// on how it was started, which is why the runtime's own answer outranks
	// this whenever one can be obtained.
	{"llama-3.1", 128000, 8192},
	{"llama3.1", 128000, 8192},
	{"llama-3.2", 128000, 8192},
	{"llama3.2", 128000, 8192},
	{"llama-3.3", 128000, 8192},
	{"llama3.3", 128000, 8192},
	{"llama-3", 8192, 4096},
	{"llama3", 8192, 4096},
	{"qwen2.5-coder", 32768, 8192},
	{"qwen2.5", 32768, 8192},
	{"qwen3-coder", 131072, 16384},
	{"qwen3", 32768, 8192},
	{"deepseek-r1", 65536, 8192},
	{"deepseek", 65536, 8192},
	{"mixtral", 32768, 8192},
	{"mistral-large", 128000, 8192},
	{"mistral", 32768, 8192},
	// Both spellings, because the runtimes disagree: Ollama tags this family
	// `gemma3:4b` and LM Studio publishes `gemma-3-4b-it-qat`. Normalization
	// collapses vendor paths and tags, not a hyphen that is part of the name,
	// so a family named two ways needs an entry for each. Found by running the
	// live suite against both runtimes on one machine.
	{"gemma3", 128000, 8192},
	{"gemma-3", 128000, 8192},
	{"gemma2", 8192, 4096},
	{"gemma-2", 8192, 4096},
	{"phi-4", 16384, 4096},
	{"devstral", 128000, 8192},
	{"codestral", 32768, 8192},
	{"glm-4", 128000, 16384},
	{"kimi-k2", 128000, 16384},
}

// limitTable returns the entries longest-prefix-first, so `claude-3-5-sonnet`
// is never answered by the `claude-` floor. Sorting here rather than requiring
// the literal above to be maintained in length order keeps a later addition
// from silently shadowing an existing entry.
func limitTable() []limitEntry {
	sorted := make([]limitEntry, len(limitEntries))
	copy(sorted, limitEntries)
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(sorted[i].prefix) > len(sorted[j].prefix)
	})
	return sorted
}

// normalizeModelID reduces the many spellings of one model to a comparable
// name.
//
// The same weights are addressed as `claude-sonnet-4-5-20250929` by Anthropic,
// `us.anthropic.claude-sonnet-4-5-20250929-v1:0` by a Bedrock inference
// profile, and `anthropic/claude-sonnet-4.5` by OpenRouter, while Ollama tags
// its own catalog `glm-5.2:cloud`. Matching the raw string would need one table
// entry per spelling, and the spelling nobody thought of is the one that ends
// up with a guessed context window.
func normalizeModelID(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return ""
	}
	// OpenRouter and Hugging Face style vendor paths.
	if index := strings.LastIndex(normalized, "/"); index >= 0 {
		normalized = normalized[index+1:]
	}
	// Ollama tags and Bedrock version suffixes alike.
	if index := strings.Index(normalized, ":"); index >= 0 {
		normalized = normalized[:index]
	}
	// Bedrock cross-region routing prefixes, then its vendor segment. Both are
	// dot-separated, and both precede the model name proper.
	for _, region := range []string{"us.", "eu.", "apac.", "global."} {
		normalized = strings.TrimPrefix(normalized, region)
	}
	if index := strings.Index(normalized, "."); index >= 0 {
		// Only a vendor segment, never a version number: `gpt-4.1` and
		// `qwen2.5` must survive this untouched.
		if vendor := normalized[:index]; isVendorSegment(vendor) {
			normalized = normalized[index+1:]
		}
	}
	return strings.TrimSpace(normalized)
}

// isVendorSegment names the dot-separated publishers Bedrock puts in front of a
// model id. It is a fixed list rather than a heuristic because the alternative
// — treating any leading alphabetic segment as a vendor — would turn `mistral`
// into an empty string and `phi-4` into itself only by luck.
func isVendorSegment(segment string) bool {
	switch segment {
	case "anthropic", "amazon", "meta", "mistral", "cohere", "ai21", "deepseek",
		"stability", "writer", "luma", "twelvelabs", "qwen", "openai", "moonshotai":
		return true
	}
	return false
}
