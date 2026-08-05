package writescope

import (
	"reflect"
	"strings"
	"testing"
)

// This package is the whole thing standing between two concurrent writers and
// each other's files, and between a writer and the parts of the repository it
// never declared. Its rules were previously exercised only incidentally
// through the graph, which is not where a scope bug would be noticed.

func TestNormalizeCanonicalizesAndRejectsEscapes(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		input []string
		want  []string
	}{
		{name: "empty writer scope is conservatively workspace-wide", input: nil, want: []string{Workspace}},
		{name: "sorted and deduplicated", input: []string{"b/", "a.go", "b/"}, want: []string{"a.go", "b/"}},
		{name: "directory absorbs its children", input: []string{"src/", "src/app/main.go", "src/app/"}, want: []string{"src/"}},
		{name: "explicit workspace marker wins", input: []string{"src/", Workspace}, want: []string{Workspace}},
		{name: "relative segments are cleaned", input: []string{"./src/./app/"}, want: []string{"src/app/"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := Normalize(testCase.input, true)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("got %v, want %v", got, testCase.want)
			}
		})
	}

	for _, testCase := range []struct{ name, scope, want string }{
		{name: "parent escape", scope: "../outside/", want: "escapes the workspace"},
		{name: "absolute path", scope: "/etc/passwd", want: "repository-relative"},
		{name: "glob", scope: "src/*.go", want: "repository-relative"},
		{name: "backslash", scope: `src\app`, want: "repository-relative"},
		{name: "newline", scope: "src/\napp", want: "repository-relative"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := Normalize([]string{testCase.scope}, true); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error=%v, want %q", err, testCase.want)
			}
		})
	}

	if _, err := Normalize([]string{"src/"}, false); err == nil {
		t.Fatal("a read-only node accepted write_paths")
	}
	tooMany := make([]string, MaxItems+1)
	for i := range tooMany {
		tooMany[i] = "path" + strings.Repeat("x", i) + "/"
	}
	if _, err := Normalize(tooMany, true); err == nil {
		t.Fatal("scope count bound was not enforced")
	}
	if _, err := Normalize([]string{strings.Repeat("a", MaxBytes+1)}, true); err == nil {
		t.Fatal("scope length bound was not enforced")
	}
}

// Overlap decides whether two writers may run at once. Its errors are only
// safe in one direction: reporting an overlap that is not real costs
// parallelism, while missing a real one lets two workers claim the same file.
func TestOverlapErrsTowardSerializing(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		left, right []string
		want        bool
	}{
		{name: "disjoint directories", left: []string{"api/"}, right: []string{"docs/"}, want: false},
		{name: "identical directories", left: []string{"api/"}, right: []string{"api/"}, want: true},
		{name: "directory contains file", left: []string{"api/"}, right: []string{"api/handler.go"}, want: true},
		{name: "workspace marker overlaps everything", left: []string{Workspace}, right: []string{"docs/"}, want: true},
		{name: "case differences are treated as a collision", left: []string{"api/"}, right: []string{"API/handler.go"}, want: true},
		{name: "shared prefix is not containment", left: []string{"api/"}, right: []string{"apiserver/"}, want: false},
		{name: "empty scope cannot collide", left: nil, right: []string{"docs/"}, want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Overlap(testCase.left, testCase.right); got != testCase.want {
				t.Fatalf("Overlap(%v, %v)=%v, want %v", testCase.left, testCase.right, got, testCase.want)
			}
			if got := Overlap(testCase.right, testCase.left); got != testCase.want {
				t.Fatalf("Overlap is not symmetric for %v / %v", testCase.left, testCase.right)
			}
		})
	}
}

// Violations decides whether a finished candidate stayed inside what it
// declared. Its errors are safe in the opposite direction from Overlap's: a
// missed violation is an undeclared write accepted as clean.
func TestViolationsErrsTowardFlagging(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		scopes  []string
		changed []string
		want    []string
	}{
		{name: "in-scope directory", scopes: []string{"api/"}, changed: []string{"api/handler.go"}, want: nil},
		{name: "exact file scope", scopes: []string{"api/handler.go"}, changed: []string{"api/handler.go"}, want: nil},
		{name: "outside scope", scopes: []string{"api/"}, changed: []string{"docs/guide.md"}, want: []string{"docs/guide.md"}},
		{name: "file scope does not cover its namesake directory", scopes: []string{"api"}, changed: []string{"api/handler.go"}, want: []string{"api/handler.go"}},
		{name: "case differences are a violation, not a match", scopes: []string{"api/"}, changed: []string{"API/handler.go"}, want: []string{"API/handler.go"}},
		{name: "sibling prefix is not containment", scopes: []string{"api/"}, changed: []string{"apiserver/main.go"}, want: []string{"apiserver/main.go"}},
		{name: "windows separators are normalized", scopes: []string{"api/"}, changed: []string{`api\handler.go`}, want: nil},
		{name: "leading ./ is normalized", scopes: []string{"api/"}, changed: []string{"./api/handler.go"}, want: nil},
		{name: "workspace-wide scope has no violations", scopes: []string{Workspace}, changed: []string{"anything.go"}, want: nil},
		{name: "results are sorted", scopes: []string{"api/"}, changed: []string{"z.go", "a.go"}, want: []string{"a.go", "z.go"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := Violations(testCase.scopes, testCase.changed)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("Violations(%v, %v)=%v, want %v", testCase.scopes, testCase.changed, got, testCase.want)
			}
		})
	}
}

// A candidate's declared scope is normalized before it is compared, so the two
// halves of the contract have to agree: anything Violations accepts as
// in-scope must belong to a scope Overlap would have serialized against.
func TestNormalizedScopesAgreeAcrossOverlapAndViolations(t *testing.T) {
	left, err := Normalize([]string{"src/app/"}, true)
	if err != nil {
		t.Fatal(err)
	}
	right, err := Normalize([]string{"src/app/models/"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !Overlap(left, right) {
		t.Fatal("nested writer scopes were not serialized")
	}
	if violations := Violations(left, []string{"src/app/models/user.go"}); violations != nil {
		t.Fatalf("a nested path was reported as out of scope: %v", violations)
	}
}
