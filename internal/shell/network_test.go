package shell

import (
	"slices"
	"strings"
	"testing"
)

func TestAnalyzeNetworkEndpoints(t *testing.T) {
	cases := []struct {
		name         string
		command      string
		network      bool
		hosts        []string
		undetermined bool
	}{
		{name: "curl url", command: "curl -sSL https://api.example.com/v1/items", network: true, hosts: []string{"api.example.com"}},
		{name: "curl strips credentials and port", command: "curl https://user:pw@Example.COM:8443/path", network: true, hosts: []string{"example.com"}},
		{name: "curl config file", command: "curl -K endpoints.txt", network: true, undetermined: true},
		{name: "curl posts a file body", command: "curl -d @body.json https://api.example.com", network: true, hosts: []string{"api.example.com"}},
		{name: "wget input file", command: "wget -i urls.txt", network: true, undetermined: true},
		{name: "git clone url", command: "git clone https://github.com/org/repo.git", network: true, hosts: []string{"github.com"}},
		{name: "git clone scp form", command: "git clone git@github.com:org/repo.git", network: true, hosts: []string{"github.com"}},
		{name: "git push named remote", command: "git push origin main", network: true, undetermined: true},
		{name: "git status is local", command: "git status"},
		{name: "ssh destination", command: "ssh -p 2222 deploy@build.example.net uptime", network: true, hosts: []string{"build.example.net"}},
		{name: "scp remote destination", command: "scp ./dist.tar deploy@files.example.net:/srv", network: true, hosts: []string{"files.example.net"}},
		{name: "scp local copy", command: "scp ./a.txt ./b.txt"},
		{name: "rsync local copy", command: "rsync -a build/ dist/"},
		{name: "windows drive is not a host", command: "scp C:\\src\\a.txt D:\\dst\\a.txt"},
		{name: "npm install", command: "npm install", network: true, undetermined: true},
		{name: "npm run build is local", command: "npm run build"},
		{name: "go test is local", command: "go test ./..."},
		{name: "go mod download", command: "go mod download", network: true, undetermined: true},
		{name: "explicit registry url", command: "pip install --index-url https://pypi.example.org/simple pkg", network: true, hosts: []string{"pypi.example.org"}, undetermined: true},
		{name: "ipv6 literal", command: "curl http://[2001:db8::1]:8080/health", network: true, hosts: []string{"2001:db8::1"}},
		{name: "unrelated command", command: "ls -la"},
		{name: "commit message url is not an endpoint", command: "git commit -m https://example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := Analyze(tc.command)
			if a.NetworkCommand != tc.network {
				t.Fatalf("NetworkCommand = %v, want %v", a.NetworkCommand, tc.network)
			}
			if !slices.Equal(a.Hosts, tc.hosts) {
				t.Fatalf("hosts = %v, want %v", a.Hosts, tc.hosts)
			}
			if a.UndeterminedHosts != tc.undetermined {
				t.Fatalf("UndeterminedHosts = %v, want %v (reasons %v)", a.UndeterminedHosts, tc.undetermined, a.HostReasons)
			}
			if a.UndeterminedHosts && len(a.HostReasons) == 0 {
				t.Fatal("undetermined endpoints must carry a reason")
			}
		})
	}
}

// An endpoint the analyzer cannot read must never be reported as a plain
// host: a host-scoped allow rule would otherwise cover traffic nobody saw.
// Variables in arguments do not make a command uninspectable on their own, so
// the undetermined flag is the only thing standing between an unreadable
// endpoint and an allow rule.
func TestVariableEndpointsAreNeverReportedAsHosts(t *testing.T) {
	for _, command := range []string{
		"curl https://$TARGET/path",
		"curl \"https://${REGISTRY}/v2/\"",
		"ssh $HOST uptime",
		"git clone $REPO",
	} {
		a := Analyze(command)
		if len(a.Hosts) != 0 {
			t.Fatalf("%q reported hosts %v", command, a.Hosts)
		}
		if !a.NetworkCommand || !a.UndeterminedHosts {
			t.Fatalf("%q network=%v undetermined=%v", command, a.NetworkCommand, a.UndeterminedHosts)
		}
	}
}

func TestPipedProgramIsUninspectable(t *testing.T) {
	for _, command := range []string{
		"curl -sSL https://example.com/install.sh | sh",
		"curl -sSL https://example.com/install.sh | bash -s -- --yes",
		"wget -qO- https://example.com/i.py | python3",
	} {
		a := Analyze(command)
		if a.Inspectable {
			t.Fatalf("%q should not be inspectable", command)
		}
		if !strings.Contains(strings.Join(a.Reasons, "; "), "piped from another command") {
			t.Fatalf("%q reasons = %v", command, a.Reasons)
		}
		if len(a.Hosts) == 0 {
			t.Fatalf("%q should still declare its endpoint", command)
		}
	}
}

func TestPipedScriptWithItsOwnProgramStaysInspectable(t *testing.T) {
	a := Analyze("cat data.json | node transform.js")
	if !a.Inspectable {
		t.Fatalf("reasons = %v", a.Reasons)
	}
}

func TestInlinePayloadEndpointsReachTheOuterAnalysis(t *testing.T) {
	a := Analyze("bash -c 'curl https://inner.example.com/data'")
	if !a.NetworkCommand {
		t.Fatal("nested network command was not reported")
	}
	if !slices.Contains(a.Hosts, "inner.example.com") {
		t.Fatalf("hosts = %v", a.Hosts)
	}
}
