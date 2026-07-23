package failureid

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEnsurePreservesMatchingAndIdentifierAcrossWrapping(t *testing.T) {
	err := Ensure(context.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("identified error no longer matches its cause")
	}
	id := ID(err)
	if !Valid(id) {
		t.Fatalf("id=%q", id)
	}
	wrapped := errors.Join(errors.New("persistence also failed"), err)
	if ID(wrapped) != id || ID(Ensure(wrapped)) != id {
		t.Fatalf("identifier changed across wrapping: %q -> %q", id, ID(wrapped))
	}
	if got := Display(err); !strings.Contains(got, "context canceled\nFailure ID: "+id) {
		t.Fatalf("display=%q", got)
	}
}

func TestEnsureNil(t *testing.T) {
	if Ensure(nil) != nil || ID(nil) != "" || Display(nil) != "" {
		t.Fatal("nil error gained correlation state")
	}
}
