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
