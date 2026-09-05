package tui

import (
	"strings"
	"testing"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/session"
)

func TestTaskContextInspectClearAndBusyGuard(t *testing.T) {
	m := newTestModel(t)
	sess := m.runtime.Session
	sess.AppendMessage(provider.Message{Role: "user", Content: "original request"})
	sess.RecordUserRequest()
	if _, err := sess.ReplaceTaskContext(0, session.TaskContext{Objective: "retained objective"}); err != nil {
		t.Fatal(err)
	}
	m.slash("/context task")
	if last := m.blocks[len(m.blocks)-1]; last.title != "Task context" || !strings.Contains(last.content, "retained objective") {
		t.Fatal("task inspector missing saved notes")
	}
	m.busy = true
	m.slash("/context clear")
	if sess.TaskContext().Revision != 1 || busySlashAllowed("/context clear") || !busySlashAllowed("/context task") {
		t.Fatal("busy clear changed notes or blocked inspection")
	}
	m.busy = false
	m.slash("/context clear")
	if c := sess.TaskContext(); c.Revision != 2 || c.Objective != "" || !strings.Contains(sess.PinnedTaskContext(), "original request") {
		t.Fatal("clear failed to preserve original request")
	}
}
