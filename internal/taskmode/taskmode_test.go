package taskmode

import "testing"

func TestParse(t *testing.T) {
	for input, want := range map[string]Mode{"": Developer, "developer": Developer, "DEVELOPER": Developer, " work ": Work} {
		got, err := Parse(input)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := Parse("assistant"); err == nil {
		t.Fatal("unknown mode was accepted")
	}
}
