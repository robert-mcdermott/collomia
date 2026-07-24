package agent

import (
	"reflect"
	"testing"
)

func TestNormalizeWriteScopes(t *testing.T) {
	got, err := NormalizeWriteScopes([]string{"docs/", "docs/guide.md", "internal/app/runtime.go"}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docs/", "internal/app/runtime.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scopes=%q want %q", got, want)
	}
	if defaulted, err := NormalizeWriteScopes(nil, true); err != nil || !reflect.DeepEqual(defaulted, []string{"*"}) {
		t.Fatalf("defaulted=%q err=%v", defaulted, err)
	}
	for _, invalid := range [][]string{{"../outside"}, {"/absolute"}, {`windows\\path`}, {"*.go"}, {"*", "../outside"}} {
		if _, err := NormalizeWriteScopes(invalid, true); err == nil {
			t.Fatalf("scope %q unexpectedly accepted", invalid)
		}
	}
	if _, err := NormalizeWriteScopes([]string{"README.md"}, false); err == nil {
		t.Fatal("read-only task accepted write_paths")
	}
}

func TestWriteScopeViolations(t *testing.T) {
	got := writeScopeViolations(
		[]string{"docs/", "README.md"},
		[]string{"docs/USER_GUIDE.md", "README.md", "internal/app/app.go"},
	)
	want := []string{"internal/app/app.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("violations=%q want %q", got, want)
	}
}
