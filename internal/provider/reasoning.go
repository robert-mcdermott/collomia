package provider

import (
	"sort"
	"strings"
)

// EffortLevels is every provider-neutral reasoning effort Collomia can send,
// in increasing order. It matches the configuration vocabulary; the order is
// what pickers display.
var EffortLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// ReasoningSupport describes which reasoning efforts one model accepts.
//
// Three states matter and must stay distinguishable. Known with levels: offer
// exactly those. Known with no levels: the model has no effort control, so
// offering one would send a field it rejects or ignores. Unknown: nothing
// established either way, so a level may be tried but must be presented as
// untested.
type ReasoningSupport struct {
	// Levels are the accepted efforts, in EffortLevels order.
	Levels []string
	// Default is the model's own level when nothing is sent, where published.
	Default string
	// Source says what established this: the endpoint or the table. Empty
	// means unknown.
	Source LimitSource
}

// Known reports whether anything established the model's effort support.
func (r ReasoningSupport) Known() bool { return r.Source != LimitsUnknown }

// Unsupported reports that the model is known to have no effort control.
func (r ReasoningSupport) Unsupported() bool { return r.Known() && len(r.Levels) == 0 }

// Supports reports whether level is known to be accepted. An unknown model
// supports nothing that can be claimed; callers decide whether to try.
func (r ReasoningSupport) Supports(level string) bool {
	for _, candidate := range r.Levels {
		if candidate == level {
			return true
		}
	}
	return false
}

// OrderedEfforts keeps only recognized effort names, deduplicated and in
// EffortLevels order. Endpoints publish model-defined names; anything outside
// the vocabulary cannot be written to configuration and is dropped.
func OrderedEfforts(names []string) []string {
	seen := map[string]bool{}
	for _, name := range names {
		seen[strings.ToLower(strings.TrimSpace(name))] = true
	}
	ordered := make([]string, 0, len(seen))
	for _, level := range EffortLevels {
		if seen[level] {
			ordered = append(ordered, level)
		}
	}
	return ordered
}

// KnownReasoning returns published effort support for a model identifier.
//
// Sources, checked 2026-09-29: OpenAI's per-model pages ("Reasoning.effort
// supports"), Anthropic's effort documentation, and Ollama's gpt-oss metadata.
// Families released after a build are deliberately absent rather than guessed:
// an unknown model is offered every level as untested, which is better than a
// wrong claim either way. Endpoints that publish support — Anthropic's model
// catalog and Ollama's /api/show — outrank this table.
func KnownReasoning(model string) (ReasoningSupport, bool) {
	normalized := normalizeModelID(model)
	if normalized == "" {
		return ReasoningSupport{}, false
	}
	for _, entry := range reasoningTable() {
		matches := strings.HasPrefix(normalized, entry.prefix)
		if entry.whole {
			matches = normalized == entry.prefix || strings.HasPrefix(normalized, entry.prefix+"-")
		}
		if matches {
			return ReasoningSupport{Levels: append([]string(nil), entry.levels...), Default: entry.fallback, Source: LimitsTable}, true
		}
	}
	return ReasoningSupport{}, false
}

type reasoningEntry struct {
	prefix   string
	levels   []string
	fallback string
	// whole matches the prefix only as a whole name or before a hyphen, so
	// "gpt-5" covers gpt-5 and gpt-5-mini but not a later gpt-5.3.
	whole bool
}

var (
	claudeFull       = []string{"low", "medium", "high", "xhigh", "max"}
	claudeNoXL       = []string{"low", "medium", "high", "max"}
	oSeries          = []string{"low", "medium", "high"}
	noReasoning      = []string{}
	reasoningEntries = []reasoningEntry{
		// Anthropic. Effort is GA on these and absent everywhere else; the
		// earlier families are listed as unsupported so they are not offered a
		// field they reject.
		{"claude-opus-4-5", []string{"low", "medium", "high"}, "high", false},
		{"claude-opus-4-6", claudeNoXL, "high", false},
		{"claude-sonnet-4-6", claudeNoXL, "high", false},
		{"claude-opus-4-7", claudeFull, "high", false},
		{"claude-opus-4-8", claudeFull, "high", false},
		{"claude-mythos-preview", claudeNoXL, "high", false},
		{"claude-opus-5-5", claudeFull, "medium", false},
		{"claude-opus-5", claudeFull, "high", false},
		{"claude-sonnet-5", claudeFull, "high", false},
		{"claude-fable-5", claudeFull, "high", false},
		{"claude-mythos-5", claudeFull, "high", false},
		{"claude-3", noReasoning, "", false},
		{"claude-sonnet-4-5", noReasoning, "", false},
		{"claude-haiku-4-5", noReasoning, "", false},
		{"claude-opus-4-1", noReasoning, "", false},
		{"claude-opus-4-0", noReasoning, "", false},
		{"claude-opus-4-2025", noReasoning, "", false},
		{"claude-sonnet-4-0", noReasoning, "", false},
		{"claude-sonnet-4-2025", noReasoning, "", false},

		// OpenAI. The gpt-5 point releases differ from one another, which is
		// why each is listed rather than matched by the family prefix.
		{"gpt-5.5", []string{"none", "low", "medium", "high", "xhigh"}, "medium", false},
		{"gpt-5.2", []string{"none", "low", "medium", "high", "xhigh"}, "none", false},
		{"gpt-5.1", []string{"none", "low", "medium", "high"}, "none", false},
		{"gpt-5", []string{"minimal", "low", "medium", "high"}, "", true},
		{"o1", oSeries, "", false},
		{"o3", oSeries, "", false},
		{"o4-mini", oSeries, "", false},
		{"gpt-4", noReasoning, "", false},
		{"gpt-3.5", noReasoning, "", false},

		// Open-weight models published with effort metadata.
		{"gpt-oss", oSeries, "medium", false},
	}
)

// reasoningTable returns the entries longest-prefix-first, as limitTable does.
func reasoningTable() []reasoningEntry {
	sorted := make([]reasoningEntry, len(reasoningEntries))
	copy(sorted, reasoningEntries)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i].prefix) > len(sorted[j].prefix) })
	return sorted
}
