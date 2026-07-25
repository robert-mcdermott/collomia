package shell

import (
	"runtime"
	"strings"
	"testing"
)

func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	return home
}

func hasCredentialTarget(a Analysis, needle string) bool {
	for _, target := range a.CredentialTargets {
		if strings.Contains(target, needle) {
			return true
		}
	}
	return false
}

// The reader is not always "cat". Any program can read a file, so the analysis
// keys on the argument rather than on a table of reading commands.
func TestCredentialTargetsAreFoundWhicheverProgramReadsThem(t *testing.T) {
	fakeHome(t)
	for _, command := range []string{
		"cat ~/.ssh/id_rsa",
		"xxd ~/.ssh/id_rsa",
		"awk '{print}' ~/.ssh/id_rsa",
		"cp ~/.ssh/id_rsa /tmp/copy",
		"base64 ~/.ssh/id_rsa",
	} {
		a := AnalyzeInWorkspace(command, "/work/repo")
		if !hasCredentialTarget(a, "SSH private key") {
			t.Errorf("%q: no credential target reported (%v)", command, a.CredentialTargets)
		}
	}
}

func TestCredentialTargetsCoverMoreThanSSHKeys(t *testing.T) {
	fakeHome(t)
	cases := map[string]string{
		"cat ~/.aws/credentials":      "AWS credentials",
		"cat .env":                    "environment file",
		"grep token ~/.npmrc":         "npm registry token",
		"cat ~/.config/gh/hosts.yml":  "GitHub CLI token",
		"cat ~/.collomia/config.json": "Collomia provider credentials",
	}
	for command, want := range cases {
		a := AnalyzeInWorkspace(command, "/work/repo")
		if !hasCredentialTarget(a, want) {
			t.Errorf("%q: want %q, got %v", command, want, a.CredentialTargets)
		}
	}
}

// An inline payload is still the outer command's reach.
func TestCredentialTargetsSurviveInlineInterpreters(t *testing.T) {
	fakeHome(t)
	a := AnalyzeInWorkspace(`bash -c "cat ~/.ssh/id_rsa"`, "/work/repo")
	if !hasCredentialTarget(a, "SSH private key") {
		t.Fatalf("nested target lost: %v", a.CredentialTargets)
	}
}

func TestCredentialTargetsSurvivePipelinesAndSequences(t *testing.T) {
	fakeHome(t)
	for _, command := range []string{
		"cat ~/.ssh/id_rsa | base64",
		"go build ./... && cat ~/.ssh/id_rsa",
		"echo start; cat ~/.ssh/id_rsa",
	} {
		a := AnalyzeInWorkspace(command, "/work/repo")
		if !hasCredentialTarget(a, "SSH private key") {
			t.Errorf("%q: target lost: %v", command, a.CredentialTargets)
		}
	}
}

// Ordinary commands must stay silent, or every build would prompt.
func TestOrdinaryCommandsReportNoCredentialTargets(t *testing.T) {
	fakeHome(t)
	for _, command := range []string{
		"go build ./...",
		"git status",
		"npm ci",
		"cat README.md",
		"cat .env.example",
		"cat ~/.ssh/id_rsa.pub",
		"ssh-keygen -y -f /dev/null",
		"grep -rn TODO internal/",
	} {
		a := AnalyzeInWorkspace(command, "/work/repo")
		if len(a.CredentialTargets) != 0 {
			t.Errorf("%q reported %v, want none", command, a.CredentialTargets)
		}
	}
}

// Reporting a target is not a decision, so it must not change how the command
// is otherwise classified. The permission layer owns the outcome.
func TestCredentialTargetsDoNotAlterInspectability(t *testing.T) {
	fakeHome(t)
	a := AnalyzeInWorkspace("cat ~/.ssh/id_rsa", "/work/repo")
	if !a.Inspectable {
		t.Fatalf("command became uninspectable: %v", a.Reasons)
	}
	if len(a.HardDenyReasons) != 0 {
		t.Fatalf("unexpected hard denial: %v", a.HardDenyReasons)
	}
	if len(a.ConfirmReasons) != 0 {
		t.Fatalf("unexpected confirmation: %v", a.ConfirmReasons)
	}
}

func TestCredentialTargetsAreDeduplicated(t *testing.T) {
	fakeHome(t)
	a := AnalyzeInWorkspace("cat ~/.ssh/id_rsa ~/.ssh/id_rsa", "/work/repo")
	if len(a.CredentialTargets) != 1 {
		t.Fatalf("targets = %v, want one entry", a.CredentialTargets)
	}
}
