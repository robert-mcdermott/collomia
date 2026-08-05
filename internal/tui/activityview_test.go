package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/robert-mcdermott/collomia/internal/activity"
	"github.com/robert-mcdermott/collomia/internal/app"
	runtimeevent "github.com/robert-mcdermott/collomia/internal/event"
)

func TestActivityViewSearchFilterCopyAndResize(t *testing.T) {
	m := newTestModel(t)
	base := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	decision := runtimeevent.Event{Schema: 1, Time: base, Kind: runtimeevent.KindPermissionDecision}
	decision.Permission = &runtimeevent.Permission{Tool: "run_command", Summary: "run unit tests", Source: "interactive", Allowed: true}
	m.handleEvent(decision)
	failure := runtimeevent.Event{Schema: 1, Time: base.Add(time.Second), Kind: runtimeevent.KindError, Error: "provider connection failed", FailureID: "err-0123456789abcdef"}
	m.handleEvent(failure)

	m.openActivityView()
	if m.activityView == nil {
		t.Fatal("activity view did not open")
	}
	view := ansi.Strip(m.renderActivityView())
	for _, want := range []string{"[success]", "run_command allowed", "[error]", "provider connection failed", failure.FailureID} {
		if !strings.Contains(view, want) {
			t.Fatalf("activity view missing %q:\n%s", want, view)
		}
	}

	updated, _ := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	for _, r := range "provider" {
		updated, _ = m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	updated, _ = m.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if len(m.activityView.matches) != 1 || m.activityView.cursor != 1 {
		t.Fatalf("activity search matches=%v cursor=%d", m.activityView.matches, m.activityView.cursor)
	}

	updated, _ = m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if !strings.Contains(m.activityView.notice, "copy unavailable") {
		t.Fatalf("copy fallback = %q", m.activityView.notice)
	}

	filters := m.activityFilters()
	for i, category := range filters {
		if category == activity.CategoryFailure {
			m.activityView.category = i
		}
	}
	m.rebuildActivityView()
	if len(m.activityView.visible) != 1 || m.activities[m.activityView.visible[0]].Category != activity.CategoryFailure {
		t.Fatalf("failure filter visible=%v", m.activityView.visible)
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = updated.(Model)
	assertScreenFits(t, m.View(), 40, 12)
}

func TestActivityRestoresFromDurableSessionWithoutReexecution(t *testing.T) {
	m := newTestModel(t)
	e := runtimeevent.New(runtimeevent.KindFileChange)
	e.File = &runtimeevent.FileChange{Path: "internal/example.go", Operation: "edit"}
	m.runtime.Session.AppendEvent(e)

	restored := New(m.runtime, NewApprovalBroker(), "")
	if len(restored.activities) != 1 {
		t.Fatalf("restored activity count=%d", len(restored.activities))
	}
	if item := restored.activities[0]; item.Category != activity.CategoryFile || !strings.Contains(item.Title, "internal/example.go") {
		t.Fatalf("restored item=%+v", item)
	}
}

func TestActivityCommandRemainsAvailableWhileBusy(t *testing.T) {
	m := newTestModel(t)
	m.handleEvent(runtimeevent.New(runtimeevent.KindTurnStart))
	m.busy = true
	m.setComposerValue("/activity")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.activityView == nil || !m.busy {
		t.Fatalf("busy activity command view=%v busy=%t", m.activityView, m.busy)
	}
	if m.input.Value() != "" {
		t.Fatalf("executed local command remained in composer: %q", m.input.Value())
	}
}

func TestReducedMotionIsOptionalAndKeepsControls(t *testing.T) {
	m := newTestModel(t)
	m.busy = true
	m.turnStarted = time.Now()
	animated := ansi.Strip(m.renderStatusBar())
	if !strings.Contains(animated, "working") || strings.Contains(animated, "• working") {
		t.Fatalf("default should retain animated progress: %q", animated)
	}

	m.runtime.Config.Options.ReducedMotion = true
	static := ansi.Strip(m.renderStatusBar())
	if !strings.Contains(static, "• working") || !strings.Contains(static, "/ local commands") || !strings.Contains(static, "esc cancel") {
		t.Fatalf("reduced-motion status lost controls: %q", static)
	}
	if !m.input.Focused() {
		t.Fatal("reduced motion must not disable composer input")
	}
}

func BenchmarkActivityView500Items(b *testing.B) {
	configureTestProvider(b)
	runtime, err := app.New(context.Background(), app.Options{Workspace: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	defer runtime.Close()
	m := New(runtime, NewApprovalBroker(), "")
	m.width, m.height, m.ready = 100, 40, true
	for i := 0; i < activity.DefaultLimit; i++ {
		m.activities = append(m.activities, activity.Item{
			Time: time.Unix(int64(i), 0), Category: activity.CategoryTool, Status: activity.StatusSuccess,
			Title: "read_file completed", Detail: fmt.Sprintf("read internal/package/file-%d.go", i),
		})
	}
	m.openActivityView()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.rebuildActivityView()
	}
}
