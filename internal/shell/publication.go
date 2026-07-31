package shell

import "strings"

// This file derives two related things from one already-normalized invocation:
// the operation it names, and whether that operation publishes.
//
// The rest of the safety classifier is a taxonomy of destruction — deletions,
// disk writes, history rewrites. That leaves a whole shape of consequence
// unclassified. `terraform destroy` requires a fresh decision while
// `terraform apply` did not; `kubectl delete` did while `kubectl apply` did
// not; `git push --force` did while `npm publish` did not. Publishing a
// package version is less reversible than deleting a Kubernetes deployment a
// controller will recreate, so the asymmetry was not a considered trade.
//
// Like the rest of the package this is a policy aid, never a boundary. It
// describes what a command's text says it will do. A program that uploads an
// artifact without naming the operation on its command line is invisible here,
// which is what OS-level confinement is for.

// Publication category labels. They name the kind of reach, not the tool, so
// a prompt reads the same whether the operation was `npm publish` or
// `cargo publish`.
const (
	publishRegistry     = "package registry"
	publishImage        = "container registry"
	publishSourceRemote = "source remote"
	publishForge        = "code forge"
	publishInfra        = "infrastructure"
	publishRemoteHost   = "remote host"
)

// operationDepth names the tools whose leading positional words decide what
// they do, and how many of those words belong to the decision. `npm` alone is
// not an action; `npm publish` is. Anything outside this table contributes no
// operation, because a rule that could name one would be naming an argument
// rather than an action.
//
// The depth counts words after the executable, and is the fullest form
// Collomia recognizes rather than the deepest form the tool accepts:
// `az webapp deployment source config` yields `az webapp deployment`. A rule
// that needs to be finer uses a glob, and `collo policy check` prints the
// exact operation string a command produces so it never has to be guessed.
var operationDepth = map[string]int{
	"git": 1, "systemctl": 1,

	"npm": 1, "pnpm": 1, "yarn": 1, "bun": 1, "pip": 1, "pip3": 1, "uv": 1,
	"poetry": 1, "pipx": 1, "conda": 1, "cargo": 1, "gem": 1, "bundle": 1,
	"composer": 1, "mvn": 1, "gradle": 1, "nuget": 1, "twine": 1,

	"apt": 1, "apt-get": 1, "brew": 1, "dnf": 1, "yum": 1, "apk": 1,
	"pacman": 1, "zypper": 1, "choco": 1, "winget": 1, "scoop": 1,

	"docker": 1, "podman": 1, "helm": 1, "kubectl": 1, "terraform": 1,
	"pulumi": 1,

	// Multi-level clients: `gh <resource> <verb>` is one action, while the
	// cloud CLIs put the service first and the verb after it.
	"gh": 2, "glab": 2, "hub": 2,
	"aws": 2, "az": 2, "gcloud": 2, "dotnet": 2, "go": 2,
}

// remoteExecution names the tools whose operation is decided by where they
// point rather than by a verb. Their operation is "<executable> <host>".
var remoteExecution = map[string]bool{
	"ssh": true, "mosh": true, "scp": true, "rsync": true, "sftp": true,
}

// classifyOperations records the operation an invocation names and whether it
// publishes. Both are reported; neither is a decision. What happens to an
// action that publishes is configurable, exactly as it is for an action that
// reaches a credential store.
func classifyOperations(inv invocation, a *Analysis) {
	operation := commandOperation(inv)
	if operation == "" {
		return
	}
	a.operation(operation)
	// The label is computed independently of the operation lookup. Deriving it
	// from the operation instead would have silently exempted every tool with
	// no subcommand — which is how `ssh prod "systemctl restart app"` first
	// passed this classifier while `npm publish` did not.
	if label := publicationLabel(inv); label != "" {
		a.publication(label + ": " + operation)
	}
}

// commandOperation renders `<executable> <verb…>` for a recognized
// subcommand-driven tool, using as many leading positionals as the tool's
// depth allows and as the command actually supplies.
func commandOperation(inv invocation) string {
	// The ssh family takes no subcommand, so the thing that decides what the
	// invocation does is its destination. Naming it here is what lets a rule
	// say "this build host, not every host" — without it the only expressible
	// exception would have been the executable, which is every host.
	if remoteExecution[inv.name] {
		hosts, readable := remoteTargets(inv.name, inv.args)
		if !readable || len(hosts) == 0 {
			return ""
		}
		return inv.name + " " + hosts[0]
	}
	depth, ok := operationDepth[inv.name]
	if !ok {
		return ""
	}
	words := operationWords(inv.name, inv.args, depth)
	if len(words) == 0 {
		// A bare invocation prints help. There is no action to name.
		return ""
	}
	return inv.name + " " + strings.Join(words, " ")
}

// operationWords collects up to depth leading positional words, skipping the
// global options that legitimately precede a subcommand and the values they
// consume.
//
// The subcommand path always comes before the first ordinary option, so an
// unrecognized option ends the collection. Continuing past one reads a flag's
// value as a verb: `aws lambda update-function-code --function-name f` yielded
// "f" as the operation and therefore matched nothing, and `gh api -X POST …`
// yielded "gh api post". Both looked like working classification.
func operationWords(name string, args []string, depth int) []string {
	var words []string
	for i := 0; i < len(args) && len(words) < depth; i++ {
		arg := args[i]
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			key := arg
			inline := false
			if eq := strings.IndexByte(key, '='); eq >= 0 {
				key, inline = key[:eq], true
			}
			skip, consumes := operationOption(name, key)
			if !skip {
				return words
			}
			if consumes && !inline {
				i++
			}
			continue
		}
		if arg == "" || strings.ContainsAny(arg, "$*?[]{}`\"'\\ \t") {
			return words
		}
		words = append(words, strings.ToLower(arg))
	}
	return words
}

// operationOption reports whether a global option may precede a subcommand and
// whether it takes a separate value. Reading a consumed value as a verb is the
// mistake `git -C /tmp status` invites.
func operationOption(name, key string) (skip, consumes bool) {
	switch name {
	case "git":
		switch key {
		case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--config-env", "--exec-path":
			return true, true
		case "--no-pager", "--paginate", "--bare", "--literal-pathspecs", "--no-replace-objects":
			return true, false
		}
	case "kubectl":
		switch key {
		case "-n", "--namespace", "--context", "--cluster", "--kubeconfig", "--user", "--as":
			return true, true
		}
	case "helm":
		switch key {
		case "-n", "--namespace", "--kube-context", "--kubeconfig", "--registry-config":
			return true, true
		case "--debug":
			return true, false
		}
	case "docker", "podman":
		switch key {
		case "-H", "--host", "--context", "--config", "--log-level":
			return true, true
		case "--debug", "-D":
			return true, false
		}
	case "terraform":
		if key == "-chdir" {
			return true, true
		}
	case "aws", "az", "gcloud":
		switch key {
		case "--profile", "--region", "--output", "--project", "--subscription", "--endpoint-url":
			return true, true
		case "--debug", "--quiet", "--no-cli-pager", "--verbose":
			return true, false
		}
	case "gh", "glab", "hub":
		if key == "-R" || key == "--repo" {
			return true, true
		}
	}
	return false, false
}

// operationVerb returns the first subcommand word, read through the same
// global-option handling the operation string uses. firstSubcommand does not
// consume option values, so `kubectl -n prod apply` reported its verb as
// "prod" — a namespace flag was enough to walk past this classifier.
func operationVerb(inv invocation) string {
	words := operationWords(inv.name, inv.args, 1)
	if len(words) == 0 {
		return ""
	}
	return words[0]
}

// publicationLabel reports the category of an invocation that puts something
// outside this machine, or "" for anything else. A rehearsal is not a
// publication: every branch declines when the command asks the tool not to
// actually do it.
func publicationLabel(inv invocation) string {
	if hasDryRun(inv.args) {
		return ""
	}
	sub := operationVerb(inv)
	switch inv.name {
	case "git":
		if sub == "push" || sub == "send-email" || sub == "request-pull" {
			return publishSourceRemote
		}
	case "npm", "pnpm", "yarn", "bun":
		if sub == "publish" {
			return publishRegistry
		}
	case "cargo", "poetry", "uv", "gem", "twine", "mvn", "gradle", "composer", "nuget":
		if publishesToRegistry(inv.name, sub) {
			return publishRegistry
		}
	case "dotnet":
		words := operationWords(inv.name, inv.args, 3)
		if len(words) >= 2 && words[0] == "nuget" && words[1] == "push" {
			return publishRegistry
		}
	case "docker", "podman":
		if sub == "push" {
			return publishImage
		}
	case "helm":
		switch sub {
		case "push":
			return publishImage
		case "install", "upgrade", "rollback":
			return publishInfra
		}
	case "gh", "glab", "hub":
		if forgeWrites(inv.args) {
			return publishForge
		}
	case "kubectl":
		switch sub {
		case "apply", "create", "replace", "patch", "edit", "rollout", "scale",
			"set", "annotate", "label", "cordon", "drain", "uncordon", "taint":
			return publishInfra
		}
	case "terraform":
		switch sub {
		case "apply", "import", "taint", "untaint", "state":
			return publishInfra
		}
	case "pulumi":
		switch sub {
		case "up", "import", "refresh":
			return publishInfra
		}
	case "aws", "az", "gcloud":
		if cloudMutates(inv.name, inv.args) {
			return publishInfra
		}
	case "ssh", "mosh":
		// A bare login is an interactive session the agent cannot drive
		// headlessly. A trailing command is arbitrary execution on another
		// machine, which is the plainest form of leaving this one.
		if _, readable := remoteTargets(inv.name, inv.args); readable && hasRemoteCommand(inv.name, inv.args) {
			return publishRemoteHost
		}
	case "scp", "rsync", "sftp":
		if hosts, _ := remoteTargets(inv.name, inv.args); len(hosts) > 0 && writesRemotely(inv.name, inv.args) {
			return publishRemoteHost
		}
	}
	return ""
}

// publishesToRegistry maps the per-tool verb that uploads a build artifact.
// They are spelled differently enough that one shared list would either miss
// `gem push` or catch `cargo push`, which does not exist.
func publishesToRegistry(name, sub string) bool {
	switch name {
	case "cargo", "poetry", "uv":
		return sub == "publish"
	case "gem", "nuget":
		return sub == "push"
	case "twine":
		return sub == "upload"
	case "mvn":
		return sub == "deploy"
	case "gradle":
		return sub == "publish" || strings.HasPrefix(sub, "publish")
	case "composer":
		return false
	}
	return false
}

// forgeWrites reports whether a GitHub/GitLab client invocation creates or
// changes something other people can see. Read verbs stay ordinary work: an
// agent that must ask before `gh pr view` is an agent nobody lets read a pull
// request.
func forgeWrites(args []string) bool {
	words := operationWords("gh", args, 2)
	if len(words) == 0 {
		return false
	}
	resource := words[0]
	verb := ""
	if len(words) > 1 {
		verb = words[1]
	}
	switch resource {
	case "api":
		// A method other than GET writes, and `gh api` defaults to GET.
		return hasWriteMethod(args)
	case "auth", "config", "completion", "alias", "extension", "status", "browse", "search":
		return false
	}
	switch verb {
	case "", "view", "list", "status", "diff", "checks", "download", "clone", "ls":
		return false
	}
	return true
}

// hasWriteMethod reports an explicit non-GET method on a raw API call.
func hasWriteMethod(args []string) bool {
	for i, arg := range args {
		value := ""
		switch {
		case arg == "-X" || arg == "--method":
			if i+1 < len(args) {
				value = args[i+1]
			}
		case strings.HasPrefix(arg, "--method="):
			value = strings.TrimPrefix(arg, "--method=")
		default:
			continue
		}
		switch strings.ToUpper(value) {
		case "", "GET", "HEAD":
			continue
		}
		return true
	}
	return false
}

// cloudMutates reports the cloud-CLI verbs that create or change a live
// resource. Deletion is deliberately absent: it is already a one-time
// confirmation through classifyAWS and its siblings, and reporting it twice
// would put the same action in two categories.
func cloudMutates(name string, args []string) bool {
	words := operationWords(name, args, 4)
	if len(words) < 2 {
		return false
	}
	// The verb's position varies by service — `gcloud run deploy svc` puts it
	// third and `aws lambda update-function-code` second — so every word after
	// the service is considered rather than only the last.
	for _, verb := range words[1:] {
		switch verb {
		case "create", "deploy", "update", "apply", "put", "set", "publish",
			"start", "restart", "invoke", "submit", "import", "restore",
			"promote", "rollback", "scale", "enable", "attach", "add", "grant":
			return true
		case "sync", "cp", "mv", "upload":
			// These run in both directions. Downloading from a bucket is an
			// ordinary read; only an upload leaves this machine.
			return uploadsToCloudStorage(args)
		}
		// AWS spells most mutations as a hyphenated operation name.
		for _, prefix := range []string{"create-", "update-", "put-", "start-", "run-", "modify-", "attach-", "associate-", "register-", "deploy-", "publish-", "import-", "restore-", "enable-", "add-", "send-", "invoke-", "upload-"} {
			if strings.HasPrefix(verb, prefix) {
				return true
			}
		}
	}
	return false
}

// uploadsToCloudStorage reports whether a copy-shaped cloud command's final
// positional is a remote URI, which is what distinguishes an upload from a
// download.
func uploadsToCloudStorage(args []string) bool {
	var positionals []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") && arg != "-" {
			continue
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) == 0 {
		return false
	}
	destination := strings.ToLower(positionals[len(positionals)-1])
	for _, scheme := range []string{"s3://", "gs://", "az://", "abfs://", "abfss://", "https://"} {
		if strings.HasPrefix(destination, scheme) {
			return true
		}
	}
	return false
}

// hasRemoteCommand reports whether an ssh-family invocation carries a command
// to run on the far side, rather than opening an interactive session.
func hasRemoteCommand(name string, args []string) bool {
	return len(networkPositionals(name, args)) > 1
}

// writesRemotely reports whether a copy tool's remote target is its
// destination rather than its source. Downloading from a server is an
// ordinary read; uploading to one leaves this machine.
func writesRemotely(name string, args []string) bool {
	positionals := networkPositionals(name, args)
	if len(positionals) < 2 {
		return false
	}
	destination := positionals[len(positionals)-1]
	_, remote := normalizeSCPTarget(destination)
	return remote
}

// hasDryRun reports the standard rehearsal switches. They are spelled
// consistently enough across these tools to share one check, and a command
// that has asked the tool not to act must not be reported as if it had.
func hasDryRun(args []string) bool {
	for _, arg := range args {
		key := arg
		value := ""
		if eq := strings.IndexByte(key, '='); eq >= 0 {
			key, value = key[:eq], strings.ToLower(key[eq+1:])
		}
		switch strings.ToLower(key) {
		case "--dry-run", "--dryrun", "--server-dry-run", "--check", "--noop", "--no-op", "--what-if":
			// `--dry-run=false` is an explicit request to act.
			return value != "false" && value != "none"
		}
	}
	return false
}
