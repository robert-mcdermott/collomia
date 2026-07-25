// Package redact removes known and likely credentials from text before it
// reaches logs, transcripts, JSONL events, permission previews, or crash
// output. Redaction is defense in depth: it reduces accidental exposure and
// is not a guarantee against a determined exfiltration attempt.
//
// Two limits are deliberate and worth stating plainly, because they decide
// what this package can and cannot be relied on for:
//
// Redaction does not sit between a tool result and the provider. An agent has
// to see the files it was asked to work on, so a secret the agent legitimately
// read still reaches the model. Keeping a credential out of the model's
// context is the permission layer's job, not this package's.
//
// Redaction is applied to bounded chunks, not to an unbounded stream. A
// credential split across two chunks can be matched in the chunk holding its
// recognizable prefix and missed in the next one.
package redact

import (
	"regexp"
	"strings"
	"sync"
)

const placeholder = "[redacted]"

// Patterns match well-known credential shapes regardless of configuration.
//
// Private key material comes first so a key body is removed whole rather than
// being partially rewritten by a later pattern that happens to match inside
// it. Every group here is non-capturing: the replacement below reads the first
// two groups as a prefix to preserve, so a capturing group added for grouping
// alone would be echoed back into the output instead of redacted.
var patterns = []*regexp.Regexp{
	// A PEM block is matched whole when both delimiters are present, and from
	// the header to the end of the text when they are not. Redaction runs on
	// bounded chunks, so a key split across two of them would otherwise pass
	// through untouched from the second chunk onward. This still cannot help a
	// chunk that contains only the body; see the package comment.
	regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]{0,40}PRIVATE KEY(?: BLOCK)?-----(?:.*?-----END [A-Z0-9 ]{0,40}PRIVATE KEY(?: BLOCK)?-----|.*)`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),                                                              // OpenAI-style keys
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{16,}`),                                                          // Anthropic keys
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                                                   // AWS access key IDs
	regexp.MustCompile(`ASIA[0-9A-Z]{16}`),                                                                   // AWS temporary key IDs
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36}`),                                                          // GitHub personal, OAuth, user, server, and refresh tokens
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{36,}`),                                                       // GitHub fine-grained PATs
	regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`),                                                           // GitLab personal access tokens
	regexp.MustCompile(`npm_[A-Za-z0-9]{36}`),                                                                // npm automation tokens
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`),                                                              // Google API keys
	regexp.MustCompile(`[sr]k_(?:live|test)_[0-9A-Za-z]{20,}`),                                               // Stripe secret and restricted keys
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),                                                       // Slack tokens
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),                      // JWTs
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/-]{16,}=*`),                                            // bearer tokens
	regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password)(["']?\s*[:=]\s*["']?)[A-Za-z0-9._~+/-]{8,}`), // key=value assignments
}

// Redactor scrubs both pattern-matched credential shapes and the exact
// secret values it has been told about (configured API keys, MCP headers).
type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

func New() *Redactor { return &Redactor{} }

// AddSecret registers an exact value to scrub. Short values are ignored so a
// secret like "x" cannot censor ordinary text.
func (r *Redactor) AddSecret(value string) {
	value = strings.TrimSpace(value)
	if len(value) < 6 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.secrets {
		if existing == value {
			return
		}
	}
	r.secrets = append(r.secrets, value)
}

func (r *Redactor) Redact(text string) string {
	if text == "" {
		return text
	}
	r.mu.RLock()
	secrets := r.secrets
	r.mu.RUnlock()
	for _, secret := range secrets {
		text = strings.ReplaceAll(text, secret, placeholder)
	}
	for _, re := range patterns {
		text = re.ReplaceAllStringFunc(text, func(match string) string {
			// Preserve a recognizable prefix (e.g. "bearer ", "api_key=")
			// captured by the first group when one exists.
			groups := re.FindStringSubmatch(match)
			if len(groups) >= 3 {
				return groups[1] + groups[2] + placeholder
			}
			if len(groups) == 2 {
				return groups[1] + placeholder
			}
			return placeholder
		})
	}
	return text
}
