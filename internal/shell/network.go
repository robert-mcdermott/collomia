package shell

import (
	"strings"
)

// This file derives the network endpoints a command was written to contact.
// Like the rest of the package it is a policy aid, never a boundary: it
// describes what the command text declares, not what the process will
// actually open. A recognized network command whose endpoints cannot be read
// statically is reported as undetermined rather than as "no hosts", so an
// allow rule can never cover traffic the analyzer could not see.

// networkKind describes how a command's endpoints are determined.
type networkKind int

const (
	// kindURL takes endpoints from URL-shaped arguments.
	kindURL networkKind = iota
	// kindTarget takes endpoints from [user@]host[:path] positionals.
	kindTarget
	// kindConfigured contacts endpoints chosen by configuration, a lockfile,
	// or a named remote. Explicit URLs in the arguments still count; the
	// remainder is undetermined.
	kindConfigured
)

// networkCommands lists commands whose documented purpose includes contacting
// a remote endpoint. Commands outside this table contribute no hosts: an
// arbitrary program can still open a socket, which is what OS-level egress
// confinement, not static analysis, is for.
var networkCommands = map[string]networkKind{
	"curl": kindURL, "wget": kindURL, "wget2": kindURL, "http": kindURL,
	"https": kindURL, "xh": kindURL, "httpie": kindURL, "aria2c": kindURL,

	"ssh": kindTarget, "sftp": kindTarget, "scp": kindTarget, "rsync": kindTarget,
	"nc": kindTarget, "ncat": kindTarget, "netcat": kindTarget, "telnet": kindTarget,
	"ftp": kindTarget, "mosh": kindTarget,

	"npm": kindConfigured, "pnpm": kindConfigured, "yarn": kindConfigured,
	"bun": kindConfigured, "npx": kindConfigured, "pip": kindConfigured,
	"pip3": kindConfigured, "uv": kindConfigured, "uvx": kindConfigured,
	"poetry": kindConfigured, "pipx": kindConfigured, "conda": kindConfigured,
	"cargo": kindConfigured, "go": kindConfigured, "gem": kindConfigured,
	"bundle": kindConfigured, "composer": kindConfigured, "mvn": kindConfigured,
	"gradle": kindConfigured, "nuget": kindConfigured, "dotnet": kindConfigured,
	"apt": kindConfigured, "apt-get": kindConfigured, "brew": kindConfigured,
	"dnf": kindConfigured, "yum": kindConfigured, "apk": kindConfigured,
	"pacman": kindConfigured, "zypper": kindConfigured, "choco": kindConfigured,
	"winget": kindConfigured, "scoop": kindConfigured,
	"docker": kindConfigured, "podman": kindConfigured, "helm": kindConfigured,
	"kubectl": kindConfigured, "terraform": kindConfigured, "pulumi": kindConfigured,
	"aws": kindConfigured, "az": kindConfigured, "gcloud": kindConfigured,
	"gh": kindConfigured, "glab": kindConfigured, "hub": kindConfigured,
}

// destinationFirst commands always contact a remote, and their first
// positional is that destination. Anything after it is a remote command and
// must never be read as a host.
var destinationFirst = map[string]bool{
	"ssh": true, "sftp": true, "telnet": true, "ftp": true, "mosh": true,
	"nc": true, "ncat": true, "netcat": true,
}

// fetchingSubcommands are the verbs that make an otherwise local tool contact
// its configured endpoints. Restricting kindConfigured commands to these verbs
// keeps ordinary local work (`npm run build`, `cargo build`) from being
// reported as network access it may never perform.
var fetchingSubcommands = map[string]bool{
	"install": true, "i": true, "add": true, "update": true, "upgrade": true,
	"fetch": true, "download": true, "get": true, "pull": true, "push": true,
	"publish": true, "restore": true, "sync": true, "ci": true, "clone": true,
	"search": true, "outdated": true, "audit": true, "login": true, "logout": true,
	"tidy": true, "mod": true, "apply": true, "init": true, "refresh": true,
	"exec": true,
}

// classifyNetwork records the endpoints one already-normalized invocation
// declares. It never changes inspectability: callers keep their own findings.
func classifyNetwork(inv invocation, a *Analysis) {
	if inv.name == "git" {
		classifyGitNetwork(inv.args, a)
		return
	}
	kind, ok := networkCommands[inv.name]
	if !ok {
		return
	}
	switch kind {
	case kindURL:
		a.NetworkCommand = true
		found := a.collectURLHosts(inv.args)
		if readsEndpointsFromFile(inv.name, inv.args) {
			a.undeterminedHost(inv.name + " reads endpoints from a file")
			return
		}
		if !found {
			a.undeterminedHost(inv.name + " endpoint could not be read from the command")
		}
	case kindTarget:
		urlHosts := a.collectURLHosts(inv.args)
		hosts, readable := remoteTargets(inv.name, inv.args)
		for _, host := range hosts {
			a.addHost(host)
		}
		switch {
		case destinationFirst[inv.name]:
			a.NetworkCommand = true
			if !readable && !urlHosts {
				a.undeterminedHost(inv.name + " destination could not be read from the command")
			}
		case len(hosts) > 0 || urlHosts:
			// A copy tool reaches the network only when one of its arguments
			// names a remote target; a purely local copy declares nothing.
			a.NetworkCommand = true
		}
	case kindConfigured:
		sub := firstSubcommand(inv.args)
		if !fetchesRemotely(inv.name, sub) {
			return
		}
		a.NetworkCommand = true
		a.collectURLHosts(inv.args)
		a.undeterminedHost(inv.name + " " + sub + " contacts endpoints chosen by configuration")
	}
}

// classifyGitNetwork reports remote access for the Git subcommands that talk
// to a server. A named remote (`origin`) resolves through repository
// configuration, so it is undetermined rather than assumed.
func classifyGitNetwork(args []string, a *Analysis) {
	rest, sub := gitSubcommand(args)
	switch sub {
	case "clone", "fetch", "pull", "push", "ls-remote", "submodule", "archive", "request-pull", "send-email", "svn":
	case "remote":
		// Only `git remote add|set-url <name> <url>` names an endpoint, and it
		// records rather than contacts it. Recording the host keeps the
		// declared endpoint visible to deny rules.
		a.collectURLHosts(rest)
		return
	default:
		return
	}
	a.NetworkCommand = true
	if a.collectURLHosts(rest) {
		return
	}
	for _, arg := range rest {
		if host, ok := normalizeSCPTarget(arg); ok {
			a.addHost(host)
			return
		}
	}
	a.undeterminedHost("git " + sub + " uses a remote configured in the repository")
}

func gitSubcommand(args []string) ([]string, string) {
	i := 0
	for i < len(args) {
		arg := args[i]
		if arg == "--" {
			i++
			break
		}
		if arg == "-C" || arg == "-c" || arg == "--git-dir" || arg == "--work-tree" || arg == "--namespace" || arg == "--config-env" {
			i += 2
			continue
		}
		if strings.HasPrefix(arg, "-") {
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return nil, ""
	}
	return args[i+1:], strings.ToLower(args[i])
}

// fetchesRemotely reports whether a configured-endpoint tool's subcommand is
// one of the verbs that reaches the network.
func fetchesRemotely(name, sub string) bool {
	if sub == "" {
		// A bare invocation prints help for every tool in this table.
		return false
	}
	switch name {
	case "go":
		// `go get`, `go install`, and `go mod download` fetch. `go build` and
		// `go test` can also fetch missing modules, but only sometimes, so they
		// are left to OS-level confinement rather than reported here.
		return sub == "get" || sub == "install" || sub == "mod" || sub == "download"
	case "docker", "podman":
		return sub == "pull" || sub == "push" || sub == "build" || sub == "login" || sub == "search" || sub == "run"
	case "gh", "glab", "hub", "aws", "az", "gcloud", "kubectl", "terraform", "pulumi", "helm":
		// Remote access is the purpose of these clients rather than one of
		// their verbs.
		return true
	}
	return fetchingSubcommands[sub]
}

func firstSubcommand(args []string) string {
	for _, arg := range args {
		if arg == "--" || strings.HasPrefix(arg, "-") {
			continue
		}
		return strings.ToLower(arg)
	}
	return ""
}

// readsEndpointsFromFile reports the curl/wget options that move the endpoint
// list out of the command text.
func readsEndpointsFromFile(name string, args []string) bool {
	for _, arg := range args {
		key := arg
		if eq := strings.IndexByte(key, '='); eq >= 0 {
			key = key[:eq]
		}
		if name == "curl" && (key == "-K" || key == "--config") {
			return true
		}
		if strings.HasPrefix(name, "wget") && (key == "-i" || key == "--input-file") {
			return true
		}
	}
	return false
}

// collectURLHosts adds every URL-shaped argument's host and reports whether
// any were found.
func (a *Analysis) collectURLHosts(args []string) bool {
	found := false
	for _, arg := range args {
		if host, ok := normalizeURLHost(arg); ok {
			a.addHost(host)
			found = true
		}
	}
	return found
}

// remoteTargets extracts destinations for the ssh-family commands. The second
// return reports whether a destination was understood, which only matters for
// commands that always contact a remote.
func remoteTargets(name string, args []string) ([]string, bool) {
	positionals := networkPositionals(name, args)
	if destinationFirst[name] {
		if len(positionals) == 0 {
			return nil, false
		}
		// sftp and ssh accept `host:path` as well as a bare destination.
		if host, ok := normalizeSCPTarget(positionals[0]); ok {
			return []string{host}, true
		}
		if host, ok := normalizeUserHost(positionals[0]); ok {
			return []string{host}, true
		}
		return nil, false
	}
	var hosts []string
	for _, arg := range positionals {
		if host, ok := normalizeSCPTarget(arg); ok {
			hosts = append(hosts, host)
		}
	}
	return hosts, len(hosts) > 0
}

// networkPositionals drops options and the values they consume so a flag's
// argument is never mistaken for a destination.
func networkPositionals(name string, args []string) []string {
	consumes := map[string]bool{
		"-o": true, "-i": true, "-l": true, "-p": true, "-P": true, "-b": true,
		"-c": true, "-m": true, "-w": true, "-s": true, "-S": true, "-F": true,
		"-J": true, "-L": true, "-R": true, "-D": true, "-W": true, "-e": true,
		"-T": true, "--rsh": true, "--port": true, "--bind-address": true,
		"--exclude": true, "--include": true, "--files-from": true,
		"--timeout": true, "--temp-dir": true,
	}
	if name == "rsync" {
		// rsync's -T is --temp-dir; its other single-letter options are flags.
		delete(consumes, "-P")
		delete(consumes, "-c")
		delete(consumes, "-i")
		delete(consumes, "-b")
		delete(consumes, "-m")
		delete(consumes, "-s")
		delete(consumes, "-D")
		delete(consumes, "-W")
	}
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			if !strings.Contains(arg, "=") && consumes[arg] {
				i++
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return positionals
}

// HostFromURL normalizes a configured endpoint URL to the same comparable
// hostname that command analysis produces, so one host rule matches a remote
// MCP server and a command that contacts it identically.
func HostFromURL(raw string) (string, bool) { return normalizeURLHost(raw) }

// NormalizeHost reduces an authority — `host`, `host:port`, or a bracketed
// IPv6 literal — to the comparable bare hostname recorded on an Analysis. The
// egress broker normalizes proxy destinations through this same function so a
// host a rule names and a host the broker dials cannot drift apart.
func NormalizeHost(authority string) (string, bool) { return normalizeHost(authority) }

// normalizeURLHost reads the host of a scheme-qualified URL argument.
func normalizeURLHost(arg string) (string, bool) {
	scheme := strings.Index(arg, "://")
	if scheme <= 0 {
		return "", false
	}
	switch strings.ToLower(arg[:scheme]) {
	case "http", "https", "ftp", "ftps", "ssh", "git", "rsync", "ws", "wss", "sftp", "scp":
	default:
		return "", false
	}
	return normalizeHost(arg[scheme+3:])
}

// normalizeSCPTarget reads the host from a `[user@]host:path` argument. A
// Windows drive letter or a plain local path is not a remote target.
func normalizeSCPTarget(arg string) (string, bool) {
	if host, ok := normalizeURLHost(arg); ok {
		return host, true
	}
	colon := strings.IndexByte(arg, ':')
	if colon <= 1 {
		// No colon, a leading colon, or a `C:\path` drive letter.
		return "", false
	}
	if strings.ContainsAny(arg[:colon], "/\\") {
		return "", false
	}
	return normalizeUserHost(arg[:colon])
}

// normalizeUserHost reads `[user@]host` without a path component.
func normalizeUserHost(arg string) (string, bool) {
	if strings.ContainsAny(arg, "/\\") {
		return "", false
	}
	return normalizeHost(arg)
}

// normalizeHost reduces an authority to a comparable lowercase hostname. It
// refuses anything it cannot read exactly — a host containing a variable,
// glob, or whitespace is reported as undetermined instead.
func normalizeHost(authority string) (string, bool) {
	value := authority
	if cut := strings.IndexAny(value, "/?#"); cut >= 0 {
		value = value[:cut]
	}
	if at := strings.LastIndexByte(value, '@'); at >= 0 {
		value = value[at+1:]
	}
	if strings.HasPrefix(value, "[") {
		// IPv6 literal: keep the address, drop the brackets and any port.
		end := strings.IndexByte(value, ']')
		if end <= 0 {
			return "", false
		}
		value = value[1:end]
	} else if colon := strings.LastIndexByte(value, ':'); colon >= 0 {
		value = value[:colon]
	}
	value = strings.TrimSuffix(strings.TrimSpace(value), ".")
	if value == "" {
		return "", false
	}
	if strings.ContainsAny(value, "$*?[]{}()`\"'\\ \t") {
		return "", false
	}
	return strings.ToLower(value), true
}

func (a *Analysis) addHost(host string) {
	for _, existing := range a.Hosts {
		if existing == host {
			return
		}
	}
	a.Hosts = append(a.Hosts, host)
}

// undeterminedHost records that a recognized network command has an endpoint
// the command text does not name. An allow rule scoped to a host must not
// cover such a request.
func (a *Analysis) undeterminedHost(reason string) {
	a.UndeterminedHosts = true
	for _, existing := range a.HostReasons {
		if existing == reason {
			return
		}
	}
	a.HostReasons = append(a.HostReasons, reason)
}
