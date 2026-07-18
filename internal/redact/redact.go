// Package redact removes known and likely credentials from text before it
// reaches logs, transcripts, JSONL events, permission previews, or crash
// output. Redaction is defense in depth: it reduces accidental exposure and
// is not a guarantee against a determined exfiltration attempt.
package redact

import (
	"regexp"
	"strings"
	"sync"
)

const placeholder = "[redacted]"

// Patterns match well-known credential shapes regardless of configuration.
var patterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),                                                              // OpenAI-style keys
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{16,}`),                                                          // Anthropic keys
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                                                   // AWS access key IDs
	regexp.MustCompile(`ASIA[0-9A-Z]{16}`),                                                                   // AWS temporary key IDs
	regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),                                                                // GitHub PATs
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{36,}`),                                                       // GitHub fine-grained PATs
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
