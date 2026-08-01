package shell

import (
	"reflect"
	"strings"
	"testing"
)

// The property this file exists for: a built-in that constructs its own argv
// must be classified exactly as the equivalent run_command string. If these
// ever diverge, a structured tool has become a way around a control that
// governs the same command typed as text.
func TestArgvAndStringPathsAgree(t *testing.T) {
	cases := [][]string{
		{"git", "commit", "-m", "message"},
		{"git", "push", "origin", "main"},
		{"git", "push", "--force", "origin", "main"},
		{"git", "checkout", "-b", "feature"},
		{"git", "reset", "--hard"},
		{"git", "clean", "-fd"},
		{"git", "add", "--", "src/main.go"},
		{"npm", "publish"},
		{"kubectl", "apply", "-f", "deploy.yaml"},
		{"docker", "push", "example/image"},
	}
	for _, argv := range cases {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			fromArgv := AnalyzeArgv(argv, "")
			fromText := Analyze(strings.Join(argv, " "))
			assertSameClassification(t, fromText, fromArgv)
		})
	}
}

func assertSameClassification(t *testing.T, want, got Analysis) {
	t.Helper()
	if !reflect.DeepEqual(want.Executables, got.Executables) {
		t.Errorf("executables: string path %v, argv path %v", want.Executables, got.Executables)
	}
	if !reflect.DeepEqual(want.Operations, got.Operations) {
		t.Errorf("operations: string path %v, argv path %v", want.Operations, got.Operations)
	}
	if !reflect.DeepEqual(want.PublicationTargets, got.PublicationTargets) {
		t.Errorf("publication targets: string path %v, argv path %v", want.PublicationTargets, got.PublicationTargets)
	}
	if !reflect.DeepEqual(want.ConfirmReasons, got.ConfirmReasons) {
		t.Errorf("confirm reasons: string path %v, argv path %v", want.ConfirmReasons, got.ConfirmReasons)
	}
	if !reflect.DeepEqual(want.CredentialTargets, got.CredentialTargets) {
		t.Errorf("credential targets: string path %v, argv path %v", want.CredentialTargets, got.CredentialTargets)
	}
	if want.Inspectable != got.Inspectable {
		t.Errorf("inspectable: string path %v, argv path %v", want.Inspectable, got.Inspectable)
	}
}

// An argv is not shell text. The characters that would carry meaning to a shell
// are ordinary bytes in an argument, and reading them as syntax would invent
// findings — the classic case being a commit message that quotes a command.
func TestArgvTreatsShellMetacharactersAsData(t *testing.T) {
	analysis := AnalyzeArgv([]string{"git", "commit", "-m", "guard against `rm -rf /` and > /dev/sda"}, "")
	if !analysis.Inspectable {
		t.Fatalf("a commit message containing shell syntax made the command uninspectable: %v", analysis.Reasons)
	}
	if len(analysis.HardDenyReasons) > 0 {
		t.Fatalf("a commit message was read as an action: %v", analysis.HardDenyReasons)
	}
	if len(analysis.ConfirmReasons) > 0 {
		t.Fatalf("a commit message was read as an action: %v", analysis.ConfirmReasons)
	}
	if want := []string{"git commit"}; !reflect.DeepEqual(analysis.Operations, want) {
		t.Fatalf("operations = %v, want %v", analysis.Operations, want)
	}
}

// The same string handed to the shell analyzer is a different thing, and must
// stay so. This is the control for the test above: the argv path is safe
// because there is no shell, not because the classifier stopped looking.
func TestSameTextAsShellCommandIsStillClassified(t *testing.T) {
	analysis := Analyze("git commit -m " + "`rm -rf /`")
	if analysis.Inspectable {
		t.Fatal("command substitution in shell text should defeat static analysis")
	}
}

func TestQuoteArgvRendersATypeableCommandLine(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"git", "commit", "-m", "simple"}, "git commit -m simple"},
		{[]string{"git", "commit", "-m", "two words"}, "git commit -m 'two words'"},
		{[]string{"git", "commit", "-m", "it's here"}, `git commit -m 'it'\''s here'`},
		{[]string{"git", "commit", "--allow-empty"}, "git commit --allow-empty"},
		{[]string{"git", "commit", "-m", ""}, "git commit -m ''"},
	} {
		if got := QuoteArgv(tc.argv); got != tc.want {
			t.Errorf("QuoteArgv(%q) = %q, want %q", tc.argv, got, tc.want)
		}
	}
}

// Publication classification is the property most worth pinning on the argv
// path, because it is the one an unclassified structured tool would bypass.
func TestArgvPushIsClassifiedAsPublication(t *testing.T) {
	analysis := AnalyzeArgv([]string{"git", "push", "origin", "main"}, "")
	if len(analysis.PublicationTargets) == 0 {
		t.Fatal("git push through the argv path reported no publication target")
	}
	if want := "source remote: git push"; analysis.PublicationTargets[0] != want {
		t.Fatalf("publication target = %q, want %q", analysis.PublicationTargets[0], want)
	}
}

func TestArgvEmptyCommandIsUninspectable(t *testing.T) {
	for _, argv := range [][]string{nil, {}, {""}} {
		analysis := AnalyzeArgv(argv, "")
		if analysis.Inspectable {
			t.Fatalf("AnalyzeArgv(%q) reported an empty command as inspectable", argv)
		}
	}
}
