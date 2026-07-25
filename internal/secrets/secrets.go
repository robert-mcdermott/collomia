// Package secrets recognizes the file locations that hold long-lived
// credentials: SSH and GPG private keys, cloud CLI token caches, registry
// authentication files, and environment files.
//
// It answers one narrow question — "does this path name a well-known
// credential store?" — and nothing else. It does not decide what happens
// next; the permission layer does that, and the answer here is deliberately
// advisory so an unrecognized location is never mistaken for a safe one.
//
// Two properties matter more than breadth:
//
// A false positive costs one approval prompt. A false negative silently hands
// a private key to a model. Where the two trade off, this package prefers the
// prompt — but not at the cost of flagging material that is routinely public,
// which is why public keys, known_hosts, and example environment files are
// excluded explicitly rather than left to chance.
package secrets

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// homeAnchored lists credential files addressed relative to the user's home
// directory. A trailing slash marks a directory whose contents all count.
var homeAnchored = []struct {
	rel   string
	label string
}{
	{".ssh/", "SSH private key material"},
	{".gnupg/", "GnuPG keyring"},
	{".aws/credentials", "AWS credentials"},
	{".azure/", "Azure CLI token cache"},
	{".config/gcloud/", "Google Cloud credentials"},
	{".kube/config", "Kubernetes cluster credentials"},
	{".docker/config.json", "Docker registry authentication"},
	{".config/gh/hosts.yml", "GitHub CLI token"},
	{".config/glab-cli/config.yml", "GitLab CLI token"},
	{".git-credentials", "stored Git credentials"},
	{".netrc", "netrc credentials"},
	{"_netrc", "netrc credentials"},
	{".npmrc", "npm registry token"},
	{".pypirc", "PyPI upload token"},
	{".cargo/credentials.toml", "crates.io token"},
	{".gem/credentials", "RubyGems token"},
	{".collomia/config.json", "Collomia provider credentials"},
	{".collomia/mcp.json", "Collomia MCP server credentials"},
}

// exemptUnderSSH lists the files inside ~/.ssh that are not secret. Public
// keys and host records are copied, appended, and inspected constantly; making
// those routine operations prompt would train users to approve without
// reading, which costs more than it protects.
var exemptUnderSSH = []string{"known_hosts", "authorized_keys", "config", "environment", "rc"}

// secretBasenames are credential files recognized wherever they appear, not
// only under the home directory. A repository checkout carries its own .npmrc
// and .env just as often as the home directory does.
var secretBasenames = map[string]string{
	".env":                 "environment file",
	".envrc":               "direnv environment file",
	".npmrc":               "npm registry token",
	".netrc":               "netrc credentials",
	"_netrc":               "netrc credentials",
	".pypirc":              "PyPI upload token",
	".git-credentials":     "stored Git credentials",
	"id_rsa":               "SSH private key",
	"id_dsa":               "SSH private key",
	"id_ecdsa":             "SSH private key",
	"id_ed25519":           "SSH private key",
	"id_ed25519_sk":        "SSH private key",
	"id_ecdsa_sk":          "SSH private key",
	"service-account.json": "service account key",
}

// secretExtensions are key and keystore formats. A .pem is often a
// certificate rather than a key, but the two are stored together often enough
// that one prompt is the cheaper error.
var secretExtensions = map[string]string{
	".pem":      "PEM key or certificate file",
	".key":      "private key file",
	".p12":      "PKCS#12 keystore",
	".pfx":      "PKCS#12 keystore",
	".jks":      "Java keystore",
	".keystore": "Java keystore",
	".ppk":      "PuTTY private key",
	".asc":      "PGP key file",
}

// publicSuffixes never hold a secret. They are checked before every rule
// below so that "id_rsa.pub" and ".env.example" stay ordinary files.
var publicSuffixes = []string{".pub", ".example", ".sample", ".template", ".dist", ".md"}

// Classify reports a short human-readable label for the credential store the
// given path names, or "" when it names none. The path may be absolute or
// relative; only its text is examined, so a path that does not exist still
// classifies. Comparison is case-insensitive on every platform: two of the
// three supported filesystems are case-insensitive by default, and treating a
// case variant as unrecognized would be a silent miss.
func Classify(candidate string) string {
	if strings.TrimSpace(candidate) == "" {
		return ""
	}
	normalized := strings.ToLower(filepath.ToSlash(filepath.Clean(candidate)))
	base := path.Base(normalized)

	// A public counterpart is never a credential, whatever else matches.
	for _, suffix := range publicSuffixes {
		if strings.HasSuffix(base, suffix) {
			return ""
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if label := classifyUnderHome(normalized, strings.ToLower(filepath.ToSlash(filepath.Clean(home)))); label != "" {
			return label
		}
	}
	if label, ok := secretBasenames[base]; ok {
		return label
	}
	// ".env.production" is a credential file; ".env.example" was already
	// excluded above as a public suffix.
	if strings.HasPrefix(base, ".env.") {
		return "environment file"
	}
	if label, ok := secretExtensions[path.Ext(base)]; ok {
		return label
	}
	return ""
}

// classifyUnderHome matches the home-anchored table. Both arguments are
// already lowercased and slash-normalized.
func classifyUnderHome(normalized, home string) string {
	rel, ok := relativeTo(normalized, home)
	if !ok {
		return ""
	}
	for _, entry := range homeAnchored {
		anchor := strings.ToLower(entry.rel)
		if strings.HasSuffix(anchor, "/") {
			if !strings.HasPrefix(rel, anchor) {
				continue
			}
			if anchor == ".ssh/" && isExemptSSHFile(path.Base(rel)) {
				return ""
			}
			return entry.label
		}
		if rel == anchor {
			return entry.label
		}
	}
	return ""
}

// relativeTo reports candidate's path relative to root, and whether candidate
// is inside root at all. It compares path segments rather than string
// prefixes, so "/home/rob-backup" is not treated as living under "/home/rob".
func relativeTo(candidate, root string) (string, bool) {
	if root == "" || candidate == root {
		return "", false
	}
	if !strings.HasSuffix(root, "/") {
		root += "/"
	}
	if !strings.HasPrefix(candidate, root) {
		return "", false
	}
	return strings.TrimPrefix(candidate, root), true
}

func isExemptSSHFile(base string) bool {
	for _, exempt := range exemptUnderSSH {
		if base == exempt || strings.HasPrefix(base, exempt+".") {
			return true
		}
	}
	return false
}

// ClassifyArgument classifies a token taken from a command line, resolving the
// shell shorthands a user actually types before matching. A token that is an
// option flag rather than a path is ignored.
//
// Expansion here is textual and intentionally incomplete: it covers the forms
// a person writes by hand, not everything a shell can produce. A command whose
// real target is hidden behind a variable or a substitution is separately
// reported as uninspectable, which already forces approval.
func ClassifyArgument(token, cwd string) string {
	token = strings.Trim(strings.TrimSpace(token), `"'`)
	if token == "" || strings.HasPrefix(token, "-") {
		return ""
	}
	expanded := expandHomeShorthand(token)
	if expanded == "" {
		return ""
	}
	if !filepath.IsAbs(expanded) && !strings.HasPrefix(filepath.ToSlash(expanded), "/") && cwd != "" {
		expanded = filepath.Join(cwd, expanded)
	}
	return Classify(expanded)
}

// expandHomeShorthand resolves the home-directory forms that appear in typed
// commands. It returns the token unchanged when no form applies.
func expandHomeShorthand(token string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return token
	}
	slashed := filepath.ToSlash(token)
	switch {
	case slashed == "~":
		return home
	case strings.HasPrefix(slashed, "~/"):
		return filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(slashed, "~/")))
	}
	for _, variable := range []string{"$HOME/", "${HOME}/", "$env:USERPROFILE/", "%USERPROFILE%/"} {
		if strings.HasPrefix(slashed, variable) {
			return filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(slashed, variable)))
		}
	}
	return token
}

// Locations returns every credential location this package recognizes, written
// the way a user would look for it: home-anchored entries keep their "~/"
// prefix, bare filenames appear as themselves, and extensions appear as
// globs. Documentation is checked against this list so the published set and
// the implemented set cannot drift apart.
func Locations() []string {
	seen := map[string]bool{}
	for _, entry := range homeAnchored {
		seen["~/"+entry.rel] = true
	}
	for name := range secretBasenames {
		seen[name] = true
	}
	for extension := range secretExtensions {
		seen["*"+extension] = true
	}
	locations := make([]string, 0, len(seen))
	for location := range seen {
		locations = append(locations, location)
	}
	sort.Strings(locations)
	return locations
}

// ExemptLocations returns the files that are deliberately not treated as
// credentials, so documentation can state the exclusions as precisely as the
// inclusions.
func ExemptLocations() []string {
	exempt := make([]string, 0, len(exemptUnderSSH)+len(publicSuffixes))
	for _, name := range exemptUnderSSH {
		exempt = append(exempt, "~/.ssh/"+name)
	}
	for _, suffix := range publicSuffixes {
		exempt = append(exempt, "*"+suffix)
	}
	sort.Strings(exempt)
	return exempt
}
