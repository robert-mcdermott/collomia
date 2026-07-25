package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

// The containment mark rides in the mode badge so it is always present, even
// on a narrow terminal where every other optional badge is dropped.
func TestStanceGlyphIsAlwaysVisible(t *testing.T) {
	m := newTestModel(t)
	for _, width := range []int{40, 80, 120} {
		m.width = width
		bar := ansi.Strip(m.renderStatusBar())
		if !strings.Contains(bar, "ASK ⛨") {
			t.Fatalf("width %d lost the containment mark: %q", width, bar)
		}
	}
}

// An indicator that pushes the cancel key off the bar has made the session
// less safe. The invariant is comparative: naming the stance must never cost
// something the same bar showed without it, at any width.
func TestStanceIndicatorNeverDisplacesRunControls(t *testing.T) {
	for _, width := range []int{40, 80, 100, 140} {
		baseline := newTestModel(t)
		baseline.busy = true
		baseline.turnStarted = time.Now()
		baseline.width = width
		before := ansi.Strip(baseline.renderStatusBar())

		named := newTestModel(t)
		named.busy = true
		named.turnStarted = time.Now()
		named.width = width
		named.runtime.Config.Permissions.Preset = appconfig.PresetHardened
		after := ansi.Strip(named.renderStatusBar())

		for _, control := range []string{"esc cancel", "working"} {
			if strings.Contains(before, control) && !strings.Contains(after, control) {
				t.Fatalf("width %d: naming the stance cost %q\nbefore: %q\nafter:  %q", width, control, before, after)
			}
		}
	}
}

// The glyph distinguishes a contained session from an uncontained one at
// every width; the word is added once there is room for it.
func TestUnsandboxedStanceIsMarkedDifferently(t *testing.T) {
	m := newTestModel(t)
	m.runtime.Config.Permissions.Preset = appconfig.PresetFrictionless
	m.runtime.Config.Permissions.Sandbox = "off"
	m.width = 80
	if bar := ansi.Strip(m.renderStatusBar()); !strings.Contains(bar, "⛉") || strings.Contains(bar, "ASK ⛨") {
		t.Fatalf("an unsandboxed session must not carry the contained mark: %q", bar)
	}
	m.width = 140
	if bar := ansi.Strip(m.renderStatusBar()); !strings.Contains(bar, "frictionless") {
		t.Fatalf("a wide bar should spell the deviating stance out: %q", bar)
	}
}

// The Session tab is the full picture, including grants handed out this
// process and the limits of what the policy layer actually enforces.
func TestSecurityContentShowsTheCompleteStance(t *testing.T) {
	m := newTestModel(t)
	m.runtime.Config.Permissions.Preset = appconfig.PresetHardened
	m.runtime.Config.Permissions.Network = "scoped"
	content := ansi.Strip(m.securityContent(m.width))
	for _, want := range []string{
		// The three groups a reader arrives with a question for.
		"Policy", "Enforcement", "This session",
		"stance", "hardened", "autonomy", "sandbox",
		"network policy", "scoped", "not egress enforcement",
		"credentials", "grants", "none",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("security section missing %q:\n%s", want, content)
		}
	}
}

// A containment setting a repository asked for and did not get reads as a bug
// until it is named, so the block says so rather than leaving the user to
// notice that config show disagrees with their project file.
func TestSecurityContentNamesRefusedProjectSettings(t *testing.T) {
	m := newTestModel(t)
	m.runtime.Config.Clamped = []appconfig.ClampedField{
		{Field: "sandbox", Requested: "off", Effective: "auto"},
	}
	content := ansi.Strip(m.securityContent(m.width))
	for _, want := range []string{"refused project", "sandbox", "off", "auto"} {
		if !strings.Contains(content, want) {
			t.Fatalf("refused setting not reported (%q missing):\n%s", want, content)
		}
	}
}

func TestDerivedStanceNamesAnUnnamedConfiguration(t *testing.T) {
	cases := map[string]appconfig.Permissions{
		"unsandboxed": {Sandbox: "off"},
		"scoped":      {Sandbox: "auto", Network: "scoped"},
		"enforced":    {Sandbox: "require"},
		"standard":    {Sandbox: "auto"},
	}
	for want, permissions := range cases {
		if got := derivedStanceLabel(permissions); got != want {
			t.Fatalf("derived label for %+v = %q, want %q", permissions, got, want)
		}
	}
}
