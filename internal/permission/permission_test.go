package permission

import (
	"context"
	"errors"
	"testing"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

func TestPermissionModes(t *testing.T) {
	requests := 0
	m := New(appconfig.Permissions{Mode: "ask"}, func(_ context.Context, _ Request) (Decision, error) { requests++; return Decision{Allow: true}, nil })
	if err := m.Authorize(t.Context(), "read_file", tools.Action{Risk: tools.RiskRead}); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatal("workspace read should not prompt")
	}
	if err := m.Authorize(t.Context(), "write_file", tools.Action{Risk: tools.RiskWrite}); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestAutopilotOutsideRequiresExplicitGrant(t *testing.T) {
	action := tools.Action{Risk: tools.RiskWrite, Outside: true}
	without := New(appconfig.Permissions{Mode: "autopilot"}, nil)
	if err := without.Authorize(t.Context(), "write_file", action); !errors.Is(err, ErrDenied) {
		t.Fatalf("expected denial, got %v", err)
	}
	with := New(appconfig.Permissions{Mode: "autopilot", AllowOutsideWorkspace: true}, nil)
	if err := with.Authorize(t.Context(), "write_file", action); err != nil {
		t.Fatal(err)
	}
}
