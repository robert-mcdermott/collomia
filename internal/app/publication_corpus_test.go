package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This is the adversarial corpus for publication — the one operation that puts
// bytes a person has not written into their own workspace.
//
// The graduation gate asks for no silent overwrite and no duplicated mutation.
// Every case below is a way the parent workspace can disagree with what a
// candidate assumed, and the required answer in each is the same: refuse and
// say why, rather than resolve it quietly. They exercise the shared helper that
// every apply path funnels through — operator, primary-agent reviewed, model
// tool, and the Orchestrated Goal's own route — so one refusal covers all four.
//
// OG-5B is why these are written down rather than trusted. Integration and the
// write tools had already drifted apart once on path handling, silently, and
// the only reason anybody noticed was that a claim about them was finally
// tested.

// publicationEscapeFixture builds a directory outside the workspace holding a
// canary whose contents must be identical afterwards.
func publicationEscapeFixture(t *testing.T) (string, string) {
	t.Helper()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "canary.txt")
	if err := os.WriteFile(outside, []byte("ORIGINAL OUTSIDE CONTENT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return outsideDir, outside
}

func requireOutsideUnchanged(t *testing.T, outside string) {
	t.Helper()
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("the canary outside the workspace is unreadable: %v", err)
	}
	if string(data) != "ORIGINAL OUTSIDE CONTENT\n" {
		t.Fatalf("publication wrote outside the workspace: %q", string(data))
	}
}

// A symlink standing where the target file should be is the direct escape: the
// bytes are addressed by a workspace-relative path, and following the link
// would put them anywhere the link points.
func TestPublicationRefusesASymlinkStandingInForATargetFile(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	_, outside := publicationEscapeFixture(t)

	data, err := os.ReadFile(fixture.delegatedFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.delegatedFile, []byte(strings.Replace(string(data), "line B", "line B from child", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.parentFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, fixture.parentFile); err != nil {
		t.Fatal(err)
	}

	applied, err := fixture.runtime.ApplyDelegateIntegration(t.Context(), "d1",
		[]DelegateIntegrationSelection{{Path: "sample.txt", Keep: []bool{true}}})
	if err == nil {
		t.Fatalf("publication followed a symlink at the target path: applied=%v", applied)
	}
	if !strings.Contains(err.Error(), "non-regular file") {
		t.Fatalf("refusal does not name the reason: %v", err)
	}
	requireOutsideUnchanged(t, outside)
}

// The directory variant is the one the file check cannot catch: nothing exists
// at the target yet, so there is no non-regular file to refuse — the escape is
// in a component of the path rather than its last element.
func TestPublicationRefusesASymlinkedDirectoryComponent(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	outsideDir, outside := publicationEscapeFixture(t)

	if err := os.MkdirAll(filepath.Join(fixture.worktree, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.worktree, "sub", "new.txt"), []byte("FROM CHILD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, fixture.worktree, "add", "sub/new.txt")
	if err := os.Symlink(outsideDir, filepath.Join(fixture.workspace, "sub")); err != nil {
		t.Fatal(err)
	}

	applied, err := fixture.runtime.ApplyDelegateIntegration(t.Context(), "d1",
		[]DelegateIntegrationSelection{{Path: "sub/new.txt", Keep: []bool{true}}})
	if err == nil {
		t.Fatalf("publication traversed a symlinked directory: applied=%v", applied)
	}
	if !strings.Contains(err.Error(), "escapes from parent") {
		t.Fatalf("refusal does not name the reason: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "new.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("a file was created outside the workspace: %v", statErr)
	}
	requireOutsideUnchanged(t, outside)
}

// Both sides created the same new path independently. There is no common
// ancestor to merge against, so any automatic answer would be a guess that
// destroys one of two authored files — and one of them is the user's.
func TestPublicationRefusesWhenBothSidesCreatedTheSamePath(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)
	const mine = "MY OWN NEW FILE\n"

	if err := os.WriteFile(filepath.Join(fixture.worktree, "new.txt"), []byte("FROM CHILD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, fixture.worktree, "add", "new.txt")
	parentCopy := filepath.Join(fixture.workspace, "new.txt")
	if err := os.WriteFile(parentCopy, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	preview, err := fixture.runtime.PrepareDelegateIntegration(t.Context(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	var conflict string
	for _, file := range preview.Files {
		if file.Path == "new.txt" {
			conflict = file.Conflict
		}
	}
	if !strings.Contains(conflict, "add or delete the same path") {
		t.Fatalf("an add/add collision was not reported as a conflict: %q", conflict)
	}
	if _, err := fixture.runtime.ApplyDelegateIntegration(t.Context(), "d1",
		[]DelegateIntegrationSelection{{Path: "new.txt", Keep: []bool{true}}}); err == nil {
		t.Fatal("an add/add collision was published")
	}
	current, err := os.ReadFile(parentCopy)
	if err != nil || string(current) != mine {
		t.Fatalf("the user's own file was overwritten: %q err=%v", string(current), err)
	}
}

// The user deleted a file the candidate modified. Restoring it with the
// candidate's edits would undo a deliberate deletion without ever saying so,
// which is the definition of a silent overwrite.
func TestPublicationRefusesWhenTheParentDeletedAModifiedFile(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)

	data, err := os.ReadFile(fixture.delegatedFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.delegatedFile, []byte(strings.Replace(string(data), "line B", "line B from child", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.parentFile); err != nil {
		t.Fatal(err)
	}

	_, err = fixture.runtime.ApplyDelegateIntegration(t.Context(), "d1",
		[]DelegateIntegrationSelection{{Path: "sample.txt", Keep: []bool{true}}})
	if err == nil {
		t.Fatal("a delete/modify divergence was published")
	}
	if !strings.Contains(err.Error(), "add or delete the same path") {
		t.Fatalf("refusal does not name the reason: %v", err)
	}
	if _, statErr := os.Stat(fixture.parentFile); !os.IsNotExist(statErr) {
		t.Fatalf("a deleted file was restored by publication: %v", statErr)
	}
}

// Publishing the same candidate twice must not apply its effect twice. The
// second attempt has nothing left to do, and saying so is the correct answer:
// a mutation that reports success without a change is how duplicates hide.
func TestPublicationOfAnAlreadyPublishedCandidateChangesNothing(t *testing.T) {
	fixture := newIntegrationFixture(t, nil)

	data, err := os.ReadFile(fixture.delegatedFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.delegatedFile, []byte(strings.Replace(string(data), "line B", "line B from child", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	selections := []DelegateIntegrationSelection{{Path: "sample.txt", Keep: []bool{true}}}
	if _, err := fixture.runtime.ApplyDelegateIntegration(t.Context(), "d1", selections); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}

	_, err = fixture.runtime.ApplyDelegateIntegration(t.Context(), "d1", selections)
	if err == nil {
		t.Fatal("a second publication of the same candidate reported success")
	}
	// The reason matters: the candidate is recognised as already present, not
	// refused by some unrelated failure that would also have hidden a duplicate.
	if !strings.Contains(err.Error(), "no delegated hunks were selected") {
		t.Fatalf("refusal does not name the reason: %v", err)
	}
	second, err := os.ReadFile(fixture.parentFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Fatalf("a second publication changed the workspace again:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
