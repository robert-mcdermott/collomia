package workspace

import "testing"

func TestParseGitStatus(t *testing.T) {
	input := "# branch.oid abcdef\r\n" +
		"# branch.head wave15\r\n" +
		"# branch.upstream origin/wave15\r\n" +
		"# branch.ab +2 -3\r\n" +
		"1 M. N... 100644 100644 100644 a b staged.go\r\n" +
		"1 .M N... 100644 100644 100644 a b modified.go\r\n" +
		"1 MM N... 100644 100644 100644 a b both.go\r\n" +
		"2 R. N... 100644 100644 100644 a b R100 renamed.go\told.go\r\n" +
		"u UU N... 100644 100644 100644 100644 a b c conflict.go\r\n" +
		"? new.go\r\n"
	got, err := ParseGitStatus(input)
	if err != nil {
		t.Fatal(err)
	}
	if !got.InRepository || got.Branch != "wave15" || got.Upstream != "origin/wave15" || got.Ahead != 2 || got.Behind != 3 {
		t.Fatalf("identity=%+v", got)
	}
	if got.Staged != 3 || got.Modified != 2 || got.Untracked != 1 || got.Conflicted != 1 {
		t.Fatalf("counts=%+v", got)
	}
}

func TestParseGitStatusDetachedAndClean(t *testing.T) {
	got, err := ParseGitStatus("# branch.oid abc\n# branch.head (detached)\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "detached HEAD" || got.Staged+got.Modified+got.Untracked+got.Conflicted != 0 {
		t.Fatalf("status=%+v", got)
	}
}
