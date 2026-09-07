package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/robert-mcdermott/collomia/internal/event"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

func contextSession(t *testing.T) (*Store, *Session) {
	t.Helper()
	store, err := OpenAt(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.New("fixture", "model")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return store, sess
}
func callContext(t *testing.T, m *ContextManager, name, args string) (string, error) {
	t.Helper()
	for _, tool := range ContextTools(m) {
		if tool.Definition().Name == name {
			return tool.Execute(t.Context(), json.RawMessage(args))
		}
	}
	t.Fatal("missing tool " + name)
	return "", nil
}
func request(sess *Session, text string) {
	sess.AppendMessage(provider.Message{Role: "user", Content: text})
	sess.RecordUserRequest()
}

func TestTaskContextSurvivesCompactionResumeForkAndRewind(t *testing.T) {
	store, sess := contextSession(t)
	request(sess, "Objective: prepare a memo for executives. Budget 900.")
	sess.AppendMessage(provider.Message{Role: "tool", Content: "SOURCE_MARKER original measurement 30 of 40"})
	if _, err := sess.ReplaceTaskContext(0, TaskContext{Objective: "Prepare memo", Sources: []ContextReference{{Reference: "m2", Note: "original observed counts"}}, NextAction: "Draft"}); err != nil {
		t.Fatal(err)
	}
	sess.AppendEvent(event.New(event.KindTurnEnd))
	request(sess, "Correction: audience volunteers; budget 250.")
	sess.AppendMessage(provider.Message{Role: "user", Content: "RUNTIME_NOTICE_NOT_A_USER_REQUEST"})
	if _, err := sess.ReplaceTaskContext(1, TaskContext{Objective: "Prepare memo", Corrections: []string{"volunteers, 250"}, Evidence: []ContextReference{{Reference: "m2", Note: "historical evidence; recheck if changed"}}, NextAction: "Validate memo"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		sess.AppendCompaction(provider.Message{Role: "user", Content: "lossy summary"}, len(sess.Active()))
	}
	sess.AppendEvent(event.New(event.KindTurnEnd))
	id := sess.Meta.ID
	sess.Close()
	loaded, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	pinned := loaded.PinnedTaskContext()
	if !strings.Contains(pinned, "volunteers; budget 250") || strings.Contains(pinned, "RUNTIME_NOTICE") || !strings.Contains(pinned, "m1:") {
		t.Fatal(pinned)
	}
	manager := NewContextManager(nil)
	manager.Use(loaded)
	out, err := callContext(t, manager, "read_session", `{"id":"m2"}`)
	if err != nil || !strings.Contains(out, "SOURCE_MARKER") {
		t.Fatalf("original evidence lost: %s %v", out, err)
	}
	fork, err := store.Fork(id)
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Close()
	if fork.TaskContext().Revision != 2 {
		t.Fatal("fork lost context")
	}
	rewound, err := store.Rewind(id, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rewound.Close()
	if rewound.TaskContext().Revision != 1 || strings.Contains(rewound.PinnedTaskContext(), "volunteers") {
		t.Fatal("rewind leaked future context")
	}
	manager.Use(rewound)
	if _, err := callContext(t, manager, "read_session", `{"id":"m3"}`); err == nil {
		t.Fatal("rewind exposed discarded message")
	}
}

func TestContextRevisionBoundsAndDurability(t *testing.T) {
	_, sess := contextSession(t)
	original := TaskContext{Constraints: []string{"Keep inputs unchanged"}}
	state, err := sess.ReplaceTaskContext(0, original)
	if err != nil {
		t.Fatal(err)
	}
	original.Constraints[0] = "mutated"
	state.Constraints[0] = "mutated"
	if sess.TaskContext().Constraints[0] != "Keep inputs unchanged" {
		t.Fatal("mutable context escaped")
	}
	if _, err := sess.ReplaceTaskContext(0, TaskContext{}); err == nil {
		t.Fatal("stale update accepted")
	}
	if _, err := sess.ReplaceTaskContext(1, TaskContext{Objective: strings.Repeat("a", 1025)}); err == nil {
		t.Fatal("oversize objective accepted")
	}
	if _, err := sess.ReplaceTaskContext(1, TaskContext{Constraints: []string{string([]byte{0xff})}}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	many := make([]string, 16)
	for i := range many {
		many[i] = strings.Repeat("x", 512)
	}
	if _, err := sess.ReplaceTaskContext(1, TaskContext{Constraints: many, Decisions: many}); err == nil {
		t.Fatal("aggregate bound ignored")
	}
	old := sess.TaskContext()
	diskErr := errors.New("disk unavailable")
	sess.file = &failedRecordFile{file: sess.file, err: diskErr}
	if _, err := sess.ReplaceTaskContext(1, TaskContext{Objective: "must not publish"}); err == nil {
		t.Fatal("disk failure hidden")
	}
	if sess.TaskContext().Revision != old.Revision || sess.Err() == nil {
		t.Fatal("failed write published or guard not latched")
	}
}

func TestContextSyncFailureDoesNotPublish(t *testing.T) {
	sess, counted := newCountedSession(t)
	counted.syncFn = func() error { return errors.New("sync failed") }
	if _, err := sess.ReplaceTaskContext(0, TaskContext{Objective: "not durable"}); err == nil {
		t.Fatal("sync failure hidden")
	}
	if sess.TaskContext().Revision != 0 || sess.Err() == nil {
		t.Fatal("failed sync published state")
	}
}

func TestSessionSearchPagingAndReadBounds(t *testing.T) {
	_, sess := contextSession(t)
	m := NewContextManager(func(s string) string { return strings.ReplaceAll(s, "SECRET", "[redacted]") })
	m.Use(sess)
	request(sess, "needle FIRST SECRET")
	sess.AppendMessage(provider.Message{Role: "tool", Content: strings.Repeat("界", 6000) + " needle LAST"})
	out, err := callContext(t, m, "search_session", `{"query":"needle","limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Next    int `json:"next_before"`
		Matches []struct {
			ID string `json:"id"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Next != 2 || result.Matches[0].ID != "m2" {
		t.Fatal(out)
	}
	request(sess, "new needle inserted while paging")
	out, err = callContext(t, m, "search_session", fmt.Sprintf(`{"query":"needle","before":%d,"limit":1}`, result.Next))
	if err != nil || !strings.Contains(out, "FIRST") || strings.Contains(out, "SECRET") {
		t.Fatalf("unstable or unredacted search: %s %v", out, err)
	}
	out, err = callContext(t, m, "read_session", `{"id":"m2","limit":5}`)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Content string `json:"content"`
		Next    int    `json:"next_offset"`
		EOF     bool   `json:"eof"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(page.Content) || page.Content != "界" || page.Next != 3 || page.EOF {
		t.Fatal(out)
	}
	if _, err := callContext(t, m, "read_session", `{"id":"../../secret"}`); err == nil {
		t.Fatal("path accepted as reference")
	}
	if _, err := callContext(t, m, "read_session", `{"id":"m2","limit":1}`); err == nil {
		t.Fatal("nonprogressing UTF-8 page allowed")
	}
	if _, err := callContext(t, m, "read_session", `{"id":"m99"}`); err == nil {
		t.Fatal("missing evidence hidden")
	}
	if _, err := callContext(t, m, "search_session", `{"query":"needle","limit":0}`); err == nil {
		t.Fatal("bad limit accepted")
	}
	if _, err := callContext(t, m, "update_task_context", `{"expected_revision":0,"objective":"saved"} {}`); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	_, other := contextSession(t)
	m.Use(other)
	out, err = callContext(t, m, "search_session", `{"query":"needle"}`)
	if err != nil || strings.Contains(out, "FIRST") {
		t.Fatal("session switch leaked evidence")
	}
	m.Use(nil)
	if _, err := callContext(t, m, "read_task_context", `{}`); err == nil {
		t.Fatal("ephemeral session offered context")
	}
}

func TestRequestPreviewsRemainBoundedAndCancellationWorks(t *testing.T) {
	_, sess := contextSession(t)
	for i := 0; i < 30; i++ {
		request(sess, fmt.Sprintf("request %d: %s", i, strings.Repeat("界", 1024)))
	}
	pinned := sess.PinnedTaskContext()
	if len(pinned) > 6500 || !strings.Contains(pinned, "request 0:") || !strings.Contains(pinned, "request 29:") || !strings.Contains(pinned, "Earlier request previews omitted") {
		t.Fatal("request retention failed")
	}
	m := NewContextManager(nil)
	m.Use(sess)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tool := range ContextTools(m) {
		if tool.Definition().Name == "search_session" {
			if _, err := tool.Execute(ctx, json.RawMessage(`{"query":"request"}`)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
}

func TestSessionSearchScanBoundAndProvenance(t *testing.T) {
	_, sess := contextSession(t)
	request(sess, "needle: actual request")
	sess.AppendMessage(provider.Message{Role: "user", Content: "needle: controller notice"})
	for i := 0; i < 2000; i++ {
		sess.AppendMessage(provider.Message{Role: "assistant", Content: "filler"})
	}
	m := NewContextManager(nil)
	m.Use(sess)
	out, err := callContext(t, m, "search_session", `{"query":"needle"}`)
	if err != nil || !strings.Contains(out, `"next_before":3`) || !strings.Contains(out, `"eof":false`) || strings.Contains(out, "actual request") {
		t.Fatalf("scan was not bounded: %s %v", out, err)
	}
	out, err = callContext(t, m, "search_session", `{"query":"needle","before":3}`)
	if err != nil || !strings.Contains(out, `"origin":"user_request"`) || !strings.Contains(out, `"origin":"runtime_or_legacy_user_role"`) || !strings.Contains(out, `"eof":true`) {
		t.Fatalf("missing provenance or continuation: %s %v", out, err)
	}
	if _, err := callContext(t, m, "read_task_context", `null`); err == nil {
		t.Fatal("null accepted instead of an object")
	}
}
