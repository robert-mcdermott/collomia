package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFilePagesBeyondOneMiB(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("1234567890\n", 100000) + "TAIL_MARKER\n"
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard(root, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := (ReadFileTool{Guard: guard}).Execute(t.Context(), json.RawMessage(`{"path":"large.txt","offset":100001,"limit":1}`))
	if err != nil || !strings.Contains(got, "100001\tTAIL_MARKER") || !strings.Contains(got, "[read_file: EOF]") {
		t.Fatalf("page=%q err=%v", got, err)
	}
}

func TestReadFilePageContinuation(t *testing.T) {
	for _, ending := range []string{"", "\n", "\r\n"} {
		input := "first\r\nsecond" + ending
		first, err := readFilePage(t.Context(), strings.NewReader(input), 1, 1)
		if err != nil || !strings.Contains(first, "continue with offset=2") {
			t.Fatalf("first=%q err=%v", first, err)
		}
		last, err := readFilePage(t.Context(), strings.NewReader(input), 2, 1)
		if err != nil || last != "     2\tsecond\n[read_file: EOF]\n" {
			t.Fatalf("last=%q err=%v", last, err)
		}
	}
	line := strings.Repeat("界", 140000)
	input := line + "\n" + line + "\n" + line
	first, err := readFilePage(t.Context(), strings.NewReader(input), 1, 5)
	if err != nil || len(first) > maxReadBytes+128 || !strings.Contains(first, "1 MiB output limit reached; continue with offset=3") {
		t.Fatalf("bytes=%d err=%v", len(first), err)
	}
	last, err := readFilePage(t.Context(), strings.NewReader(input), 3, 5)
	if err != nil || !strings.Contains(last, "     3\t"+line+"\n[read_file: EOF]") {
		t.Fatalf("last bytes=%d err=%v", len(last), err)
	}
}

func TestReadFileOversizedLinesAndCancellation(t *testing.T) {
	for _, size := range []int{maxReadBytes, maxReadBytes + 100} {
		page, err := readFilePage(t.Context(), strings.NewReader("first\n"+strings.Repeat("x", size)+"\ntail"), 1, 3)
		if err != nil || !strings.Contains(page, "     1\tfirst") || !strings.Contains(page, "oversized line 2; read offset=2") {
			t.Fatalf("lost the earlier page before an oversized line: bytes=%d err=%v", len(page), err)
		}
	}
	input := strings.Repeat("x", maxReadBytes+100) + "\ntail"
	_, err := readFilePage(t.Context(), strings.NewReader(input), 1, 1)
	if err == nil || !strings.Contains(err.Error(), "offset=2") {
		t.Fatalf("missing oversized-line guidance: %v", err)
	}
	page, err := readFilePage(t.Context(), strings.NewReader(input), 2, 1)
	if err != nil || !strings.Contains(page, "     2\ttail") {
		t.Fatalf("cannot skip oversized line: page=%q err=%v", page, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := readFilePage(ctx, strings.NewReader(input), 2, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	for _, offset := range []int{1, 200000} {
		page, err := readFilePage(t.Context(), strings.NewReader(""), offset, 1)
		if err != nil || !strings.Contains(page, fmt.Sprintf("EOF before offset=%d", offset)) {
			t.Fatalf("empty=%q err=%v", page, err)
		}
	}
}
