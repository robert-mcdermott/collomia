package session

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCheckpointDeltasAndCompletionRoundTrip(t *testing.T) {
	store, sess := contextSession(t)
	states := []string{
		`{"schema_version":1,"root_identity":"root","entries":[{"Path":"a","Before":"AQID"}]}`,
		`{"schema_version":1,"root_identity":"root","entries":[{"Path":"a","Before":"AQID"},{"Path":"b","Before":"BAUG"}]}`,
		`{"schema_version":1,"root_identity":"root","entries":[{"Path":"b","Before":"BAUG"},{"Path":"c","Before":"Bw=="}]}`,
		`{"schema_version":1,"root_identity":"root","restore_interrupted":true,"entries":[{"Path":"b","Before":"BAUG"}]}`,
	}
	for _, state := range states {
		if err := sess.SaveWorkspaceCheckpoint(json.RawMessage(state)); err != nil {
			t.Fatal(err)
		}
	}
	if err := sess.SaveCompletion(json.RawMessage(`{"schema_version":1,"dirty":true,"paths":["report.txt"]}`)); err != nil {
		t.Fatal(err)
	}
	id := sess.Meta.ID
	sess.Close()
	resumed, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if got := string(resumed.LoadWorkspaceCheckpoint()); !strings.Contains(got, `"Path":"b"`) || strings.Contains(got, `"Path":"a"`) || !strings.Contains(got, "restore_interrupted") {
		t.Fatal(got)
	}
	if !strings.Contains(string(resumed.LoadCompletion()), "report.txt") {
		t.Fatal("lost completion obligations")
	}
	if _, err := applyCheckpointDelta(nil, json.RawMessage(`{"schema_version":1,"keep":10,"header":{}}`)); err == nil {
		t.Fatal("invalid delta references accepted")
	}
	if _, err := applyCheckpointDelta(nil, json.RawMessage(`{"schema_version":9}`)); err == nil {
		t.Fatal("future schema accepted")
	}
}

func TestRecoverySyncFailureDoesNotPublishState(t *testing.T) {
	sess, file := newCountedSession(t)
	file.syncFn = func() error { return errors.New("sync failed") }
	if err := sess.SaveCompletion(json.RawMessage(`{"schema_version":1,"dirty":true}`)); err == nil {
		t.Fatal("failed sync accepted")
	}
	if len(sess.LoadCompletion()) != 0 {
		t.Fatal("unflushed state published")
	}
	if sess.Err() == nil {
		t.Fatal("failure not latched")
	}
}
