package diffmodel

import (
	"strings"
	"testing"
)

func FuzzDiffAndHunkParsing(f *testing.F) {
	f.Add("one\ntwo\n", "one\nchanged\n")
	f.Add("", "created\n")
	f.Fuzz(func(t *testing.T, before, after string) {
		if len(before)+len(after) > 32<<10 || strings.Count(before, "\n")+strings.Count(after, "\n") > 200 {
			t.Skip()
		}
		diff := Unified("fixture.txt", before, after)
		hunks, err := ParseHunks(diff)
		if err == nil {
			keep := make([]bool, len(hunks))
			for i := range keep {
				keep[i] = true
			}
			_, _ = ApplyHunks(before, hunks, keep)
		}
		_ = Align(before, after)
	})
}
