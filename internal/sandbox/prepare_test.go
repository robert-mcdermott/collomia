package sandbox

import (
	"errors"
	"strings"
	"testing"
)

type fixtureBackend struct {
	caps      Capabilities
	available error
	wrapErr   error
}

func (f fixtureBackend) Name() string               { return "fixture" }
func (f fixtureBackend) Capabilities() Capabilities { return f.caps }
func (f fixtureBackend) Available() error           { return f.available }
func (f fixtureBackend) Wrap(argv []string, _ Policy) ([]string, error) {
	if f.wrapErr != nil {
		return nil, f.wrapErr
	}
	return append([]string{"wrapped"}, argv...), nil
}

func TestPrepareRequireRejectsMissingNetworkProtection(t *testing.T) {
	backend := fixtureBackend{caps: Capabilities{WriteIsolation: true, NetworkIsolation: NetworkTCP}}
	_, err := Prepare(backend, ModeRequire, []string{"command"}, Policy{WorkspaceRoot: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "UDP network denial") {
		t.Fatalf("require must fail on partial network enforcement, got %v", err)
	}
}

func TestPrepareRequireRejectsMissingReadProtection(t *testing.T) {
	backend := fixtureBackend{caps: Capabilities{WriteIsolation: true, NetworkIsolation: NetworkFull}}
	_, err := Prepare(backend, ModeRequire, []string{"command"}, Policy{WorkspaceRoot: t.TempDir(), AllowNetwork: true, ConstrainReads: true})
	if err == nil || !strings.Contains(err.Error(), "user-data read confinement") {
		t.Fatalf("require must fail on missing read enforcement, got %v", err)
	}
}

func TestPrepareBroadReadsDoNotRequireReadIsolation(t *testing.T) {
	backend := fixtureBackend{caps: Capabilities{WriteIsolation: true, NetworkIsolation: NetworkFull}}
	got, err := Prepare(backend, ModeRequire, []string{"command"}, Policy{WorkspaceRoot: t.TempDir(), AllowNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Active || got.Degraded != "" {
		t.Fatalf("preparation=%+v", got)
	}
}

func TestPrepareAutoUsesPartialBackendAndWarns(t *testing.T) {
	backend := fixtureBackend{caps: Capabilities{WriteIsolation: true, NetworkIsolation: NetworkTCP}}
	got, err := Prepare(backend, ModeAuto, []string{"command"}, Policy{WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Active || len(got.Argv) != 2 || got.Degraded == "" {
		t.Fatalf("preparation=%+v", got)
	}
}

func TestPrepareAutoContinuesWhenUnavailable(t *testing.T) {
	got, err := Prepare(fixtureBackend{available: errors.New("missing")}, ModeAuto, []string{"command"}, Policy{WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Active || !strings.Contains(got.Degraded, "normal user privileges") {
		t.Fatalf("preparation=%+v", got)
	}
}

func TestCapabilitiesSummaryIsExplicit(t *testing.T) {
	got := (Capabilities{WriteIsolation: true, ReadIsolation: true, NetworkIsolation: NetworkTCP}).Summary()
	for _, want := range []string{"workspace write confinement", "user-data read confinement available", "TCP denial only"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q missing %q", got, want)
		}
	}
}

func TestReadPolicySummaryDistinguishesRequestedAndAlwaysOn(t *testing.T) {
	optional := Capabilities{ReadIsolation: true}
	if got := optional.ReadPolicySummary(Policy{}); got != "broad" {
		t.Fatalf("optional broad summary=%q", got)
	}
	if got := optional.ReadPolicySummary(Policy{ConstrainReads: true}); got != "workspace-scoped" {
		t.Fatalf("optional confined summary=%q", got)
	}
	always := Capabilities{ReadIsolation: true, ReadIsolationAlways: true}
	if got := always.ReadPolicySummary(Policy{}); got != "confined (backend-enforced)" {
		t.Fatalf("always-on summary=%q", got)
	}
}
