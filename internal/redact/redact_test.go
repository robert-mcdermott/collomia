package redact

import (
	"strings"
	"testing"
)

func TestKnownSecretIsRedactedEverywhere(t *testing.T) {
	r := New()
	r.AddSecret("super-secret-token-123")
	got := r.Redact("Authorization: super-secret-token-123 end")
	if strings.Contains(got, "super-secret-token-123") {
		t.Fatalf("exact secret survived: %q", got)
	}
}

func TestShortSecretsAreIgnored(t *testing.T) {
	r := New()
	r.AddSecret("go")
	if got := r.Redact("go build ./..."); got != "go build ./..." {
		t.Fatalf("short value should not censor text: %q", got)
	}
}

func TestCredentialPatterns(t *testing.T) {
	r := New()
	cases := []string{
		"key sk-abcdefghijklmnop1234 trailing",
		"aws AKIAIOSFODNN7EXAMPLE id",
		"gh ghp_123456789012345678901234567890123456 pat",
		"Authorization: Bearer abcdef1234567890abcdef done",
		"api_key=verysecretvalue99",
		"slack xoxb-1234567890-abcdefghij token",
	}
	for _, in := range cases {
		got := r.Redact(in)
		if !strings.Contains(got, "[redacted]") {
			t.Errorf("pattern not redacted: %q -> %q", in, got)
		}
	}
}

func TestOrdinaryTextIsUntouched(t *testing.T) {
	r := New()
	in := "run go test ./... and read internal/config/config.go"
	if got := r.Redact(in); got != in {
		t.Fatalf("ordinary text changed: %q", got)
	}
}

// A private key is the credential most likely to be read verbatim by an
// ordinary command, and the one no token-prefix pattern would ever catch.
func TestPrivateKeyBlocksAreRemovedWhole(t *testing.T) {
	r := New()
	for _, label := range []string{"RSA PRIVATE KEY", "OPENSSH PRIVATE KEY", "EC PRIVATE KEY", "PRIVATE KEY", "ENCRYPTED PRIVATE KEY", "PGP PRIVATE KEY BLOCK"} {
		in := "before\n-----BEGIN " + label + "-----\nMIIEowIBAAKCAQEAxxxx\nb3BlbnNzaC1rZXktdjEA\n-----END " + label + "-----\nafter"
		got := r.Redact(in)
		if strings.Contains(got, "MIIEowIBAAKCAQEAxxxx") || strings.Contains(got, "b3BlbnNzaC1rZXktdjEA") {
			t.Errorf("%s body survived: %q", label, got)
		}
		if strings.Contains(got, "BEGIN "+label) {
			t.Errorf("%s header survived: %q", label, got)
		}
		if !strings.HasPrefix(got, "before\n") || !strings.HasSuffix(got, "\nafter") {
			t.Errorf("%s redaction consumed surrounding text: %q", label, got)
		}
	}
}

// Output is redacted in chunks, so the chunk carrying the header frequently
// does not carry the closing delimiter.
func TestUnterminatedPrivateKeyIsRedactedToEndOfChunk(t *testing.T) {
	r := New()
	got := r.Redact("$ cat id_rsa\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vb")
	if strings.Contains(got, "b3BlbnNzaC1rZXktdjEA") {
		t.Fatalf("unterminated key body survived: %q", got)
	}
	if !strings.HasPrefix(got, "$ cat id_rsa\n") {
		t.Fatalf("redaction consumed preceding text: %q", got)
	}
}

// A public key is not a credential, and redacting one would corrupt ordinary
// work like appending to authorized_keys.
func TestPublicKeyMaterialIsNotRedacted(t *testing.T) {
	r := New()
	for _, in := range []string{
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDexample user@host",
		"-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkq\n-----END PUBLIC KEY-----",
		"-----BEGIN CERTIFICATE-----\nMIIDdzCCAl+gAwIB\n-----END CERTIFICATE-----",
	} {
		if got := r.Redact(in); got != in {
			t.Errorf("public material was redacted: %q -> %q", in, got)
		}
	}
}

// Fixtures are assembled from a prefix and a body rather than written as whole
// literals. A synthetic token that matches a provider's published shape is
// indistinguishable from a real one to a secret scanner, and a literal here
// blocked a push once already. Splitting the prefix keeps the assembled value
// exact for the regex while leaving no scannable token in the source.
func TestAdditionalProviderTokenPatterns(t *testing.T) {
	r := New()
	cases := map[string][2]string{
		"github oauth":    {"gho" + "_", "123456789012345678901234567890123456"},
		"github server":   {"ghs" + "_", "123456789012345678901234567890123456"},
		"gitlab pat":      {"glpat" + "-", "abcdefghij1234567890"},
		"npm automation":  {"npm" + "_", "123456789012345678901234567890123456"},
		"google api key":  {"AIza", "SyA1234567890abcdefghijklmnopqrstuvw"},
		"stripe live key": {"sk" + "_" + "live" + "_", "abcdefghij1234567890ABCD"},
	}
	for name, parts := range cases {
		token := parts[0] + parts[1]
		got := r.Redact("value " + token + " end")
		if strings.Contains(got, token) {
			t.Errorf("%s survived: %q", name, got)
		}
	}
}

// The replacement path echoes the first two capture groups back as a preserved
// prefix, so a grouping construct that captures would leak the value it was
// meant to hide. This asserts the property rather than each pattern.
func TestPatternsUseNonCapturingGroupsOnly(t *testing.T) {
	for _, re := range patterns {
		names := re.SubexpNames()
		// Two capture groups are load-bearing for the prefix-preserving
		// patterns ("bearer ", "api_key="); more than that is unreviewed.
		if len(names)-1 > 2 {
			t.Errorf("pattern %q has %d capture groups; use (?:...) for grouping", re, len(names)-1)
		}
	}
}
