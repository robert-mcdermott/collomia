package diffmodel

import (
	"strings"
	"testing"
)

func allTrue(n int) []bool {
	keep := make([]bool, n)
	for i := range keep {
		keep[i] = true
	}
	return keep
}
func allFalse(n int) []bool {
	return make([]bool, n)
}

func TestParseHunksAndApplyAllReproducesAfter(t *testing.T) {
	before := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\neleven\ntwelve\nthirteen\nfourteen\nfifteen\n"
	after := "one\nTWO\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\neleven\ntwelve\nTHIRTEEN\nfourteen\nfifteen\n"
	diff := Unified("f.txt", before, after)
	hunks, err := ParseHunks(diff)
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) != 2 {
		t.Fatalf("expected 2 separate hunks (changes are far apart), got %d:\n%s", len(hunks), diff)
	}
	got, err := ApplyHunks(before, hunks, allTrue(len(hunks)))
	if err != nil {
		t.Fatal(err)
	}
	if got != after {
		t.Fatalf("applying all hunks should reproduce after exactly:\ngot:  %q\nwant: %q", got, after)
	}
}

func TestApplyHunksNoneReproducesBefore(t *testing.T) {
	before := "one\ntwo\nthree\n"
	after := "one\nTWO\nthree\n"
	diff := Unified("f.txt", before, after)
	hunks, err := ParseHunks(diff)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ApplyHunks(before, hunks, allFalse(len(hunks)))
	if err != nil {
		t.Fatal(err)
	}
	if got != before {
		t.Fatalf("keeping no hunks should reproduce before exactly:\ngot:  %q\nwant: %q", got, before)
	}
}

func TestApplyHunksSelective(t *testing.T) {
	before := "alpha\nbeta\ngamma\ndelta\nepsilon\nzeta\neta\ntheta\niota\nkappa\nlambda\nmu\nnu\nxi\nomicron\n"
	after := "alpha\nBETA\ngamma\ndelta\nepsilon\nzeta\neta\ntheta\niota\nkappa\nlambda\nmu\nNU\nxi\nomicron\n"
	diff := Unified("f.txt", before, after)
	hunks, err := ParseHunks(diff)
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(hunks))
	}
	// Keep only the first hunk (beta -> BETA); reject the second (nu -> NU).
	got, err := ApplyHunks(before, hunks, []bool{true, false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "BETA") {
		t.Fatalf("expected the kept hunk applied: %q", got)
	}
	if strings.Contains(got, "NU") {
		t.Fatalf("expected the rejected hunk left as original: %q", got)
	}
	if !strings.Contains(got, "\nnu\n") {
		t.Fatalf("rejected hunk should leave original line intact: %q", got)
	}
}

func TestParseHunksRejectsNonDiff(t *testing.T) {
	if _, err := ParseHunks("not a diff at all"); err == nil {
		t.Fatal("expected an error for text with no hunk headers")
	}
}

func TestApplyHunksRejectsMismatchedKeepLength(t *testing.T) {
	diff := Unified("f.txt", "a\n", "b\n")
	hunks, err := ParseHunks(diff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyHunks("a\n", hunks, []bool{true, true}); err == nil {
		t.Fatal("expected a mismatched-length error")
	}
}
