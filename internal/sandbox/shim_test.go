package sandbox

import "testing"

func TestDispatchReexecIgnoresOrdinaryArguments(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"-test.v"}, {"doctor"}} {
		handled, err := DispatchReexec(args)
		if handled || err != nil {
			t.Fatalf("DispatchReexec(%q) = handled %t, err %v", args, handled, err)
		}
	}
}

func TestDispatchReexecRecognizesSandboxEntryPoints(t *testing.T) {
	for _, name := range []string{"__landlock", "__appcontainer"} {
		handled, err := DispatchReexec([]string{name})
		if !handled {
			t.Fatalf("DispatchReexec(%q) did not recognize the sandbox entry point", name)
		}
		if err == nil {
			t.Fatalf("DispatchReexec(%q) accepted a malformed invocation", name)
		}
	}
}
