package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPatchRejectsMisplacedAndMissingContentBeforeAnyMutation(t *testing.T) {
	for _, bad := range []string{
		`{"op":"update","path":"manifest","old_text":"BEFORE","new_text":"","content":"AFTER"}`,
		`{"op":"update","path":"manifest","old_text":"BEFORE","content":"AFTER"}`,
		`{"op":"update","path":"manifest","old_text":"BEFORE"}`,
		`{"op":"update","path":"manifest","old_text":"BEFORE","new_text":null}`,
		`{"op":"create","path":"new-file","new_text":"AFTER"}`,
		`{"op":"create","path":"new-file"}`,
		`{"op":"delete","path":"manifest","content":"AFTER"}`,
		`{"op":"update","path":"manifest","old_text":"BEFORE","replacement":"AFTER"}`,
	} {
		t.Run(bad, func(t *testing.T) {
			tool, dir := patchTool(t)
			for _, path := range []string{"manifest", "other"} {
				if err := os.WriteFile(filepath.Join(dir, path), []byte("BEFORE"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			raw := json.RawMessage(`{"operations":[{"op":"delete","path":"other"},` + bad + `]}`)
			if _, err := tool.Assess(raw); !IsInputError(err) {
				t.Fatalf("assessment accepted invalid patch: %v", err)
			}
			if _, err := tool.Execute(t.Context(), raw); !IsInputError(err) {
				t.Fatalf("execution accepted invalid patch: %v", err)
			}
			for _, path := range []string{"manifest", "other"} {
				got, err := os.ReadFile(filepath.Join(dir, path))
				if err != nil || string(got) != "BEFORE" {
					t.Fatalf("malformed batch mutated %s: %q %v", path, got, err)
				}
			}
		})
	}
}

func TestPatchAllowsExplicitEmptyReplacementAndEmptyCreation(t *testing.T) {
	tool, dir := patchTool(t)
	if err := os.WriteFile(filepath.Join(dir, "manifest"), []byte("BEFORE"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"operations":[{"op":"update","path":"manifest","old_text":"BEFORE","new_text":""},{"op":"create","path":"empty","content":""}]}`)
	if _, err := tool.Execute(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"manifest", "empty"} {
		got, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil || len(got) != 0 {
			t.Fatalf("intentional empty file %s: %q %v", path, got, err)
		}
	}
}

func TestFileToolsRequireExplicitReplacementText(t *testing.T) {
	patch, dir := patchTool(t)
	for _, tc := range []struct {
		tool Tool
		bad  []string
		good string
	}{
		{WriteFileTool{Guard: patch.Guard}, []string{`{"path":"file"}`, `{"path":"file","content":null}`, `{"path":"file","new_text":"AFTER"}`}, `{"path":"file","content":""}`},
		{EditFileTool{Guard: patch.Guard}, []string{`{"path":"file","old_text":"BEFORE"}`, `{"path":"file","old_text":"BEFORE","new_text":null}`, `{"path":"file","old_text":"BEFORE","new_text":"","content":"AFTER"}`}, `{"path":"file","old_text":"BEFORE","new_text":""}`},
	} {
		t.Run(tc.tool.Definition().Name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "file"), []byte("BEFORE"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, raw := range tc.bad {
				if _, err := tc.tool.Assess(json.RawMessage(raw)); !IsInputError(err) {
					t.Fatalf("assessment accepted %s: %v", raw, err)
				}
				if _, err := tc.tool.Execute(t.Context(), json.RawMessage(raw)); !IsInputError(err) {
					t.Fatalf("execution accepted %s: %v", raw, err)
				}
				got, err := os.ReadFile(filepath.Join(dir, "file"))
				if err != nil || string(got) != "BEFORE" {
					t.Fatalf("invalid input changed file: %q %v", got, err)
				}
			}
			if _, err := tc.tool.Execute(t.Context(), json.RawMessage(tc.good)); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(dir, "file"))
			if err != nil || len(got) != 0 {
				t.Fatalf("explicit empty replacement rejected: %q %v", got, err)
			}
		})
	}
}
