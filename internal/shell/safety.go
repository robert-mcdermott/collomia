package shell

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// classifySegment adds outcome-aware safety findings for one simple command.
// It returns the effective directory for following segments so the common
// `cd build && rm -rf *` form is evaluated against build rather than the
// workspace root.
func classifySegment(tokens []string, cwd string, a *Analysis) string {
	inv, uncertain := normalizeInvocation(tokens)
	if uncertain != "" {
		a.confirm(uncertain)
	}
	if inv.name == "" {
		return cwd
	}
	if isInlineInterpreter(inv.name) {
		if payload, encoded := inlinePayload(inv.name, inv.args); payload != "" {
			nested := analyzeAt(payload, a.workspace, cwd)
			mergeSafety(a, nested)
		} else if encoded {
			a.confirm(inv.name + " encoded command requires one-time approval")
		}
		return cwd
	}
	if inv.name == "cd" || inv.name == "chdir" {
		args := inv.args
		if len(args) > 0 && strings.EqualFold(args[0], "/d") {
			args = args[1:]
		}
		if len(args) > 0 {
			if next, ok := resolveStaticPath(args[0], cwd); ok {
				return next
			}
		}
		return cwd
	}

	switch inv.name {
	case "rm", "remove-item", "ri":
		classifyRM(inv.args, cwd, a)
	case "rmdir", "rd":
		classifyRmdir(inv.args, cwd, a)
	case "del", "erase":
		classifyDel(inv.args, cwd, a)
	case "find":
		classifyFind(inv.args, cwd, a)
	case "chmod", "chown", "chgrp", "chattr":
		classifyRecursiveMetadata(inv.name, inv.args, cwd, a)
	case "git":
		classifyGit(inv.args, a)
	case "shutdown", "reboot", "halt", "poweroff", "stop-computer", "restart-computer":
		a.confirm(inv.name + " changes the machine lifecycle")
	case "systemctl":
		if containsAnyFold(inv.args, "reboot", "poweroff", "halt", "rescue", "emergency") {
			a.confirm("systemctl lifecycle operation")
		}
	case "mkfs", "mkswap", "wipefs":
		classifyFilesystemBuilder(inv.name, inv.args, cwd, a)
	case "parted", "sgdisk":
		classifyPartitioner(inv.name, inv.args, cwd, a)
	case "fdisk", "cfdisk":
		if !containsAnyFold(inv.args, "-l", "--list") {
			for _, target := range likelyDeviceTargets(inv.args) {
				if resolved, known := resolveStaticPath(target, cwd); !known {
					a.confirm(inv.name + " target cannot be resolved statically: " + target)
				} else if isRawDevice(resolved) {
					a.confirm(inv.name + " can modify a physical disk: " + target)
				}
			}
		}
	case "dd":
		classifyDD(inv.args, cwd, a)
	case "cp", "mv", "install", "sponge":
		classifyWriteDestination(inv.name, inv.args, cwd, a)
	case "truncate", "fallocate":
		classifyWriteDestination(inv.name, inv.args, cwd, a)
	case "shred", "blkdiscard":
		classifyRawStorageTargets(inv.name, inv.args, cwd, a)
	case "tee":
		classifyTee(inv.args, cwd, a)
	case "diskutil":
		classifyDiskutil(inv.args, cwd, a)
	case "format", "format.com", "clear-disk", "format-volume", "remove-partition", "remove-volume":
		classifyDiskCommand(inv.name, inv.args, cwd, a)
	case "diskpart", "diskpart.exe":
		classifyDiskpart(inv.args, cwd, a)
	case "terraform":
		if containsAnyFold(inv.args, "destroy", "-destroy") {
			a.confirm("terraform bulk infrastructure destruction")
		}
	case "kubectl":
		if containsAnyFold(inv.args, "delete") {
			a.confirm("kubectl resource deletion")
		}
	case "aws":
		classifyAWS(inv.args, a)
	case "az":
		classifyAzureCLI(inv.args, a)
	case "gcloud":
		classifyGCloud(inv.args, a)
	case "pulumi":
		if containsAnyFold(inv.args, "destroy") {
			a.confirm("Pulumi bulk infrastructure destruction")
		}
	case "helm":
		if containsAnyFold(inv.args, "uninstall", "delete") {
			a.confirm("Helm release deletion")
		}
	case "docker", "podman":
		if containsAnyFold(inv.args, "system", "volume", "builder") && containsAnyFold(inv.args, "prune") {
			a.confirm(inv.name + " bulk cleanup")
		}
	case "zpool", "zfs", "lvremove", "vgremove", "pvremove":
		if inv.name == "zpool" && containsAnyFold(inv.args, "destroy") || inv.name == "zfs" && containsAnyFold(inv.args, "destroy") || strings.HasSuffix(inv.name, "remove") {
			a.confirm(inv.name + " destructive storage operation")
		}
	case "psql", "mysql", "mysqlsh", "sqlcmd":
		if argsContainSQLDestruction(inv.args) {
			a.confirm(inv.name + " database destruction")
		}
	case "xargs":
		if containsAnyFold(inv.args, "rm", "rmdir", "del", "erase", "remove-item") {
			a.confirm("xargs deletion targets cannot be determined statically")
		}
	default:
		if strings.HasPrefix(inv.name, "mkfs.") {
			classifyFilesystemBuilder(inv.name, inv.args, cwd, a)
		}
	}
	return cwd
}

type invocation struct {
	name string
	args []string
}

// normalizeInvocation exposes commands behind common transparent wrappers.
func normalizeInvocation(tokens []string) (invocation, string) {
	i := 0
	for i < len(tokens) && isAssignment(tokens[i]) {
		i++
	}
	if i >= len(tokens) {
		return invocation{}, ""
	}
	current := append([]string(nil), tokens[i:]...)
	for depth := 0; depth < 8 && len(current) > 0; depth++ {
		name := strings.ToLower(filepath.Base(current[0]))
		args := current[1:]
		next, wrapped, uncertain := wrapperTarget(name, args)
		if !wrapped {
			return invocation{name: name, args: args}, uncertain
		}
		if next < 0 || next >= len(args) {
			return invocation{name: name, args: args}, uncertain
		}
		current = args[next:]
	}
	return invocation{}, "wrapper nesting is too deep to analyze safely"
}

func wrapperTarget(name string, args []string) (int, bool, string) {
	switch name {
	case "sudo":
		return skipWrapperOptions(args, map[string]bool{"-u": true, "--user": true, "-g": true, "--group": true, "-h": true, "--host": true, "-p": true, "--prompt": true, "-C": true, "--close-from": true, "-T": true, "--command-timeout": true, "-r": true, "--role": true, "-t": true, "--type": true}), true, ""
	case "doas":
		return skipWrapperOptions(args, map[string]bool{"-u": true}), true, ""
	case "env":
		i := 0
		for i < len(args) {
			arg := args[i]
			if arg == "--" {
				return i + 1, true, ""
			}
			if isAssignment(arg) {
				i++
				continue
			}
			if arg == "-u" || arg == "--unset" || arg == "-C" || arg == "--chdir" {
				i += 2
				continue
			}
			if strings.HasPrefix(arg, "--unset=") || strings.HasPrefix(arg, "--chdir=") || arg == "-i" || arg == "--ignore-environment" || arg == "-0" || arg == "--null" {
				i++
				continue
			}
			if arg == "-S" || arg == "--split-string" || strings.HasPrefix(arg, "--split-string=") {
				return -1, true, "env --split-string command requires one-time approval"
			}
			if strings.HasPrefix(arg, "-") {
				i++
				continue
			}
			return i, true, ""
		}
		return -1, true, ""
	case "command":
		for i, arg := range args {
			if arg == "-v" || arg == "-V" {
				return -1, false, ""
			}
			if arg == "--" {
				return i + 1, true, ""
			}
			if !strings.HasPrefix(arg, "-") {
				return i, true, ""
			}
		}
		return -1, true, ""
	case "nohup", "time":
		return skipWrapperOptions(args, nil), true, ""
	case "nice":
		return skipWrapperOptions(args, map[string]bool{"-n": true, "--adjustment": true}), true, ""
	case "stdbuf":
		return skipWrapperOptions(args, map[string]bool{"-i": true, "--input": true, "-o": true, "--output": true, "-e": true, "--error": true}), true, ""
	case "timeout":
		i := skipWrapperOptions(args, map[string]bool{"-k": true, "--kill-after": true, "-s": true, "--signal": true})
		if i < len(args) {
			i++ // duration
		}
		return i, true, ""
	default:
		return -1, false, ""
	}
}

func skipWrapperOptions(args []string, consumes map[string]bool) int {
	i := 0
	for i < len(args) {
		arg := args[i]
		if arg == "--" {
			return i + 1
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			return i
		}
		key := arg
		if eq := strings.IndexByte(key, '='); eq >= 0 {
			key = key[:eq]
		}
		if consumes[key] && !strings.Contains(arg, "=") {
			i += 2
		} else {
			i++
		}
	}
	return i
}

func isInlineInterpreter(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "ksh", "dash", "fish", "cmd", "cmd.exe", "pwsh", "powershell", "powershell.exe":
		return true
	default:
		return false
	}
}

func inlinePayload(name string, args []string) (payload string, encoded bool) {
	for i, arg := range args {
		lower := strings.ToLower(arg)
		switch {
		case (name == "cmd" || name == "cmd.exe") && lower == "/c" && i+1 < len(args):
			return strings.Join(args[i+1:], " "), false
		case (name == "pwsh" || strings.HasPrefix(name, "powershell")) && (lower == "-command" || lower == "-c") && i+1 < len(args):
			return strings.Join(args[i+1:], " "), false
		case (name == "pwsh" || strings.HasPrefix(name, "powershell")) && lower == "-encodedcommand":
			return "", true
		case lower == "-c" && i+1 < len(args):
			return args[i+1], false
		}
	}
	return "", false
}

func mergeSafety(dst *Analysis, src Analysis) {
	for _, reason := range src.HardDenyReasons {
		dst.hardDeny(reason)
	}
	for _, reason := range src.ConfirmReasons {
		dst.confirm(reason)
	}
}

func classifyRM(args []string, cwd string, a *Analysis) {
	recursive, noPreserve := false, false
	var targets []string
	options := true
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if options && arg == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(arg, "-") && arg != "-" {
			switch lower {
			case "--recursive":
				recursive = true
			case "--no-preserve-root":
				noPreserve = true
			default:
				if !strings.HasPrefix(arg, "--") && strings.ContainsAny(arg[1:], "rR") {
					recursive = true
				}
			}
			continue
		}
		// GNU option parsing permits options after operands; recognize those
		// too unless an explicit -- ended option processing.
		if options && strings.HasPrefix(arg, "-") {
			continue
		}
		targets = append(targets, arg)
	}
	if recursive && noPreserve {
		a.hardDeny("recursive rm disables root preservation")
	}
	for _, target := range targets {
		if isCriticalWriteTarget(target, cwd, a.workspace) {
			a.hardDeny("rm targets protected configuration or system state: " + target)
			continue
		}
		protected, known, label := protectedRemovalTarget(target, cwd, a.workspace)
		if recursive && protected {
			a.hardDeny("recursive rm targets protected root " + label)
		} else if recursive && !known {
			a.confirm("recursive rm target cannot be resolved statically: " + target)
		}
	}
}

func classifyRmdir(args []string, cwd string, a *Analysis) {
	recursive := false
	var targets []string
	for _, arg := range args {
		if strings.EqualFold(arg, "/s") || strings.EqualFold(arg, "--recursive") {
			recursive = true
			continue
		}
		if strings.EqualFold(arg, "/q") {
			continue
		}
		targets = append(targets, arg)
	}
	if !recursive {
		return
	}
	for _, target := range targets {
		protected, known, label := protectedRemovalTarget(target, cwd, a.workspace)
		if protected {
			a.hardDeny("recursive rmdir targets protected root " + label)
		} else if !known {
			a.confirm("recursive rmdir target cannot be resolved statically: " + target)
		}
	}
}

func classifyDel(args []string, cwd string, a *Analysis) {
	for _, target := range nonOptionArgs(args, "/") {
		protected, known, label := protectedRemovalTarget(target, cwd, a.workspace)
		if protected {
			a.hardDeny("del targets protected root " + label)
		} else if !known {
			a.confirm("del target cannot be resolved statically: " + target)
		}
	}
}

func classifyFind(args []string, cwd string, a *Analysis) {
	deleteAction := containsAnyFold(args, "-delete")
	execDelete := containsAnyFold(args, "-exec", "-execdir", "-ok", "-okdir") && containsAnyFold(args, "rm", "rmdir", "remove-item", "del", "erase")
	if !deleteAction && !execDelete {
		return
	}
	if execDelete {
		a.confirm("find executes deletion with dynamically selected targets")
	}
	if !deleteAction {
		return
	}
	selective := containsAnyFold(args, "-name", "-iname", "-path", "-ipath", "-regex", "-type", "-links", "-size", "-user", "-group")
	var roots []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") || arg == "!" || arg == "(" {
			break
		}
		roots = append(roots, arg)
	}
	if len(roots) == 0 {
		roots = []string{"."}
	}
	for _, root := range roots {
		protected, known, label := protectedRemovalTarget(root, cwd, a.workspace)
		if protected && !selective {
			a.hardDeny("find -delete traverses protected root " + label)
		} else if protected || !known {
			a.confirm("find -delete requires one-time approval for target " + root)
		}
	}
}

func classifyRecursiveMetadata(name string, args []string, cwd string, a *Analysis) {
	recursive := containsShortOrLongOption(args, 'r', "--recursive") || containsShortOrLongOption(args, 'R', "--recursive")
	if !recursive {
		return
	}
	if containsAnyFold(args, "--no-preserve-root") {
		a.hardDeny(name + " recursively disables root preservation")
	}
	targets := metadataTargets(name, args)
	for _, target := range targets {
		protected, known, label := protectedRemovalTarget(target, cwd, a.workspace)
		if protected {
			a.hardDeny("recursive " + name + " targets protected root " + label)
		} else if !known {
			a.confirm("recursive " + name + " target cannot be resolved statically: " + target)
		}
	}
}

func metadataTargets(name string, args []string) []string {
	plain := nonOptionArgs(args, "-")
	if len(plain) < 2 {
		return nil
	}
	// chmod/chown/chgrp/chattr consume a mode/owner/attribute before paths.
	return plain[1:]
}

func classifyGit(args []string, a *Analysis) {
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
		if strings.HasPrefix(arg, "--git-dir=") || strings.HasPrefix(arg, "--work-tree=") || strings.HasPrefix(arg, "--namespace=") || strings.HasPrefix(arg, "--config-env=") {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return
	}
	sub := strings.ToLower(args[i])
	rest := args[i+1:]
	switch sub {
	case "reset":
		if containsAnyFold(rest, "--hard") {
			a.confirm("git reset --hard can discard uncommitted work")
		}
	case "clean":
		if !containsShortOrLongOption(rest, 'n', "--dry-run") {
			a.confirm("git clean can permanently delete untracked files")
		}
	case "restore":
		if !containsAnyFold(rest, "--staged") || containsAnyFold(rest, "--worktree") {
			a.confirm("git restore can discard working-tree changes")
		}
	case "checkout":
		if containsAnyFold(rest, "--") || containsShortOrLongOption(rest, 'f', "--force") {
			a.confirm("git checkout can discard working-tree changes")
		}
	case "branch":
		if containsShortOrLongOption(rest, 'D', "--delete-force") {
			a.confirm("forced Git branch deletion")
		}
	case "push":
		forceRefspec := false
		for _, arg := range rest {
			forceRefspec = forceRefspec || strings.HasPrefix(arg, "+")
		}
		if forceRefspec || containsShortOrLongOption(rest, 'f', "--force") || containsAnyFold(rest, "--force-with-lease", "--delete") {
			a.confirm("forced Git push rewrites remote history")
		}
	case "switch":
		if containsAnyFold(rest, "--discard-changes", "--force", "-f") {
			a.confirm("Git switch can discard working-tree changes")
		}
	case "stash":
		if containsAnyFold(rest, "drop", "clear") {
			a.confirm("Git stash deletion")
		}
	case "reflog":
		if containsAnyFold(rest, "expire", "delete") {
			a.confirm("Git reflog deletion can remove recovery points")
		}
	case "gc":
		if containsAnyFold(rest, "--prune=now", "--aggressive") {
			a.confirm("Git garbage collection can remove recovery objects")
		}
	case "filter-branch", "filter-repo":
		a.confirm("Git history rewrite")
	case "worktree":
		if containsAnyFold(rest, "remove") && containsAnyFold(rest, "--force", "-f") {
			a.confirm("forced Git worktree removal")
		}
	}
}

func classifyFilesystemBuilder(name string, args []string, cwd string, a *Analysis) {
	if name == "wipefs" && containsAnyFold(args, "-n", "--no-act") {
		return
	}
	for _, target := range likelyDeviceTargets(args) {
		resolved, known := resolveStaticPath(target, cwd)
		if !known {
			a.confirm(name + " target cannot be resolved statically: " + target)
			continue
		}
		if isRawDevice(resolved) {
			a.hardDeny(name + " would overwrite a physical disk or device: " + target)
		}
	}
}

func classifyPartitioner(name string, args []string, cwd string, a *Analysis) {
	destructive := containsAnyFold(args, "mklabel", "mktable", "mkpart", "rm", "resizepart", "--zap-all", "--clear", "--delete", "--new")
	if !destructive {
		return
	}
	for _, target := range likelyDeviceTargets(args) {
		resolved, known := resolveStaticPath(target, cwd)
		if !known {
			a.confirm(name + " target cannot be resolved statically: " + target)
		} else if isRawDevice(resolved) {
			a.hardDeny(name + " would destructively modify a physical disk: " + target)
		}
	}
}

func classifyDD(args []string, cwd string, a *Analysis) {
	for _, arg := range args {
		if !strings.HasPrefix(strings.ToLower(arg), "of=") {
			continue
		}
		target := arg[3:]
		resolved, known := resolveStaticPath(target, cwd)
		if !known {
			a.confirm("dd output target cannot be resolved statically: " + target)
		} else if isRawDevice(resolved) || isCriticalWriteTarget(resolved, cwd, a.workspace) {
			a.hardDeny("dd would overwrite protected storage: " + target)
		}
	}
}

func classifyWriteDestination(name string, args []string, cwd string, a *Analysis) {
	targets := nonOptionArgs(args, "-")
	if len(targets) == 0 {
		return
	}
	target := targets[len(targets)-1]
	resolved, known := resolveStaticPath(target, cwd)
	if !known {
		return
	}
	if isRawDevice(resolved) || isCriticalWriteTarget(resolved, cwd, a.workspace) {
		a.hardDeny(name + " would overwrite protected storage or safety state: " + target)
	}
}

func classifyRawStorageTargets(name string, args []string, cwd string, a *Analysis) {
	for _, target := range nonOptionArgs(args, "-") {
		resolved, known := resolveStaticPath(target, cwd)
		if !known {
			a.confirm(name + " target cannot be resolved statically: " + target)
		} else if isRawDevice(resolved) {
			a.hardDeny(name + " would destructively modify a physical disk or device: " + target)
		}
	}
}

func classifyDiskutil(args []string, cwd string, a *Analysis) {
	if !containsAnyFold(args, "eraseDisk", "eraseVolume", "partitionDisk", "secureErase", "deleteContainer", "deleteVolume") {
		return
	}
	for _, target := range args {
		if diskIdentifierRE.MatchString(target) {
			a.hardDeny("diskutil would erase or repartition physical storage: " + target)
			return
		}
		resolved, known := resolveStaticPath(target, cwd)
		if known && isRawDevice(resolved) {
			a.hardDeny("diskutil would erase or repartition physical storage: " + target)
			return
		}
	}
	a.confirm("diskutil destructive storage operation")
}

func classifyTee(args []string, cwd string, a *Analysis) {
	for _, target := range nonOptionArgs(args, "-") {
		resolved, known := resolveStaticPath(target, cwd)
		if !known {
			continue
		}
		if isRawDevice(resolved) || isCriticalWriteTarget(resolved, cwd, a.workspace) {
			a.hardDeny("tee would overwrite protected storage: " + target)
		}
	}
}

func classifyDiskCommand(name string, args []string, cwd string, a *Analysis) {
	if name == "format" || name == "format.com" {
		for _, arg := range args {
			if isWindowsVolumeTarget(arg) {
				a.hardDeny("format would erase Windows volume " + arg)
				return
			}
		}
		a.confirm("format target requires one-time approval")
		return
	}
	// These PowerShell storage cmdlets operate on disks, volumes, or
	// partitions rather than regular workspace files.
	a.hardDeny(name + " would destructively modify disk or volume state")
}

func classifyDiskpart(args []string, cwd string, a *Analysis) {
	for i, arg := range args {
		if (strings.EqualFold(arg, "/s") || strings.EqualFold(arg, "-s")) && i+1 < len(args) {
			path, known := resolveStaticPath(args[i+1], cwd)
			if !known {
				a.confirm("DiskPart script cannot be resolved statically")
				return
			}
			data, err := os.ReadFile(path)
			if err != nil {
				a.confirm("DiskPart script cannot be inspected")
				return
			}
			for _, line := range strings.Split(strings.ToLower(string(data)), "\n") {
				line = strings.TrimSpace(line)
				if line == "clean" || line == "clean all" || strings.HasPrefix(line, "delete ") || strings.HasPrefix(line, "format ") {
					a.hardDeny("DiskPart script contains destructive disk command: " + line)
					return
				}
			}
			a.confirm("DiskPart script changes storage configuration")
			return
		}
	}
	a.confirm("interactive DiskPart operation")
}

func classifyAWS(args []string, a *Analysis) {
	args = skipLeadingCLIOptions(args, map[string]bool{
		"--profile": true, "--region": true, "--endpoint-url": true, "--output": true, "--query": true,
		"--ca-bundle": true, "--cli-connect-timeout": true, "--cli-read-timeout": true,
	})
	if len(args) < 2 {
		return
	}
	service, sub := strings.ToLower(args[0]), strings.ToLower(args[1])
	if service == "s3" && (sub == "rm" && containsAnyFold(args[2:], "--recursive") || sub == "rb") {
		a.confirm("AWS S3 bulk deletion")
		return
	}
	for _, destructive := range []struct{ service, operation string }{
		{"ec2", "terminate-instances"}, {"rds", "delete-db-instance"}, {"rds", "delete-db-cluster"},
		{"cloudformation", "delete-stack"}, {"eks", "delete-cluster"}, {"route53", "delete-hosted-zone"},
	} {
		if service == destructive.service && sub == destructive.operation {
			a.confirm("AWS destructive resource operation: " + service + " " + sub)
			return
		}
	}
}

func classifyAzureCLI(args []string, a *Analysis) {
	args = skipLeadingCLIOptions(args, map[string]bool{
		"--subscription": true, "--tenant": true, "--output": true, "-o": true,
	})
	if len(args) < 2 {
		return
	}
	joined := strings.ToLower(strings.Join(args, " "))
	if strings.HasPrefix(joined, "group delete ") || strings.HasPrefix(joined, "group delete") ||
		strings.HasPrefix(joined, "vm delete ") || strings.HasPrefix(joined, "aks delete ") ||
		strings.HasPrefix(joined, "account management-group delete ") {
		a.confirm("Azure destructive resource operation")
	}
}

func classifyGCloud(args []string, a *Analysis) {
	args = skipLeadingCLIOptions(args, map[string]bool{
		"--project": true, "--account": true, "--configuration": true, "--impersonate-service-account": true,
	})
	joined := strings.ToLower(strings.Join(args, " "))
	for _, operation := range []string{"projects delete", "compute instances delete", "container clusters delete", "sql instances delete"} {
		if strings.HasPrefix(joined, operation+" ") || joined == operation {
			a.confirm("Google Cloud destructive resource operation: " + operation)
			return
		}
	}
}

func skipLeadingCLIOptions(args []string, consumesValue map[string]bool) []string {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		arg := args[i]
		key := arg
		if eq := strings.IndexByte(key, '='); eq >= 0 {
			key = key[:eq]
		}
		if consumesValue[key] && !strings.Contains(arg, "=") && i+1 < len(args) {
			i += 2
		} else {
			i++
		}
	}
	return args[i:]
}

func argsContainSQLDestruction(args []string) bool {
	joined := strings.ToUpper(strings.Join(args, " "))
	return strings.Contains(joined, "DROP DATABASE") || strings.Contains(joined, "DROP SCHEMA") || strings.Contains(joined, "TRUNCATE TABLE")
}

var redirectionRE = regexp.MustCompile(`(?:^|[\s;|&])\d*>+\s*("[^"]+"|'[^']+'|[^\s;|&]+)`)

var (
	windowsPhysicalRE  = regexp.MustCompile(`(?i)\\\\[.?]\\(?:physicaldrive\d+|globalroot\\device\\harddisk)`)
	windowsRecursiveRE = regexp.MustCompile(`(?i)(?:^|\s)(?:-recurse\b|/s\b|-[a-z]*r[a-z]*\b)`)
	windowsDriveGlobRE = regexp.MustCompile(`(?i)^[a-z]:/(?:\*|\.\*|\.\?\?\*)$`)
	windowsVolumeRE    = regexp.MustCompile(`(?i)^[a-z]:(?:[\\/])?$`)
	rawSegmentSplitRE  = regexp.MustCompile(`[;&|\r\n]+`)
	diskIdentifierRE   = regexp.MustCompile(`(?i)^disk\d+(?:s\d+)*$`)
)

// classifyRawWindows preserves backslashes that the POSIX-oriented tokenizer
// treats as escapes. It covers cmd.exe and PowerShell drive-root accidents on
// every build host, which also makes the behavior testable cross-platform.
func classifyRawWindows(command string, a *Analysis) {
	for _, segment := range rawSegmentSplitRE.Split(command, -1) {
		lower := strings.ToLower(strings.TrimSpace(segment))
		if lower == "" {
			continue
		}
		deletion := startsWithCommand(lower, "rm", "ri", "remove-item", "rmdir", "rd", "del", "erase") ||
			(strings.Contains(lower, " -command ") && containsCommandWord(lower, "rm", "ri", "remove-item", "rmdir", "rd", "del", "erase"))
		if deletion && windowsRecursiveRE.MatchString(lower) && hasWindowsProtectedWord(segment) {
			a.hardDeny("recursive deletion targets a protected Windows drive or system root")
		}
		if windowsPhysicalRE.MatchString(segment) && containsCommandWord(lower, "format", "clear-disk", "format-volume", "remove-partition", "remove-volume", "dd", "shred") {
			a.hardDeny("command would destructively modify a physical Windows device")
		}
	}
}

func hasWindowsProtectedWord(segment string) bool {
	for _, field := range rawWords(segment) {
		candidates := append([]string{field}, strings.Fields(field)...)
		for _, candidate := range candidates {
			candidate = strings.Trim(candidate, `"'(),;`)
			if isWindowsProtectedTarget(candidate) {
				return true
			}
			normalized := strings.ToLower(strings.ReplaceAll(candidate, `\`, "/"))
			if normalized == "$home" || normalized == "%userprofile%" || normalized == "$env:userprofile" ||
				strings.HasPrefix(normalized, "$home/*") || strings.HasPrefix(normalized, "%userprofile%/*") || strings.HasPrefix(normalized, "$env:userprofile/*") {
				return true
			}
		}
	}
	return false
}

func rawWords(value string) []string {
	var words []string
	var word strings.Builder
	var quote rune
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, r := range value {
		if (r == '\'' || r == '"') && (quote == 0 || quote == r) {
			if quote == 0 {
				quote = r
			} else {
				quote = 0
			}
			continue
		}
		if (r == ' ' || r == '\t') && quote == 0 {
			flush()
			continue
		}
		word.WriteRune(r)
	}
	flush()
	return words
}

func startsWithCommand(segment string, names ...string) bool {
	fields := rawWords(segment)
	if len(fields) == 0 {
		return false
	}
	first := strings.Trim(strings.ToLower(filepath.Base(strings.Trim(fields[0], `"'`))), `"'`)
	for _, name := range names {
		if first == name {
			return true
		}
	}
	return false
}

func containsCommandWord(value string, names ...string) bool {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z')
	})
	for _, field := range fields {
		for _, name := range names {
			if field == name {
				return true
			}
		}
	}
	return false
}

func classifyRedirections(command, cwd string, a *Analysis) {
	for _, match := range redirectionRE.FindAllStringSubmatch(command, -1) {
		if len(match) < 2 {
			continue
		}
		target := strings.Trim(match[1], `"'`)
		resolved, known := resolveStaticPath(target, cwd)
		if !known {
			continue
		}
		if isRawDevice(resolved) || isCriticalWriteTarget(resolved, cwd, a.workspace) {
			a.hardDeny("shell redirection would overwrite protected storage: " + target)
		}
	}
}

func protectedRemovalTarget(target, cwd, workspace string) (protected, known bool, label string) {
	expanded, ok := expandStaticTarget(target)
	if !ok {
		return false, false, target
	}
	if isWindowsProtectedTarget(expanded) {
		return true, true, expanded
	}
	resolved, ok := resolveStaticPath(expanded, cwd)
	if !ok {
		return false, false, target
	}
	normalized := filepath.Clean(resolved)
	candidates := []string{normalized}
	if canonical, ok := canonicalRemovalTarget(normalized); ok && !samePathText(canonical, normalized) {
		candidates = append(candidates, canonical)
	}
	for _, candidate := range candidates {
		if workspace != "" {
			gitState := filepath.Join(workspace, ".git")
			if insideOrSame(gitState, candidate) {
				return true, true, gitState
			}
		}
		for _, root := range protectedRoots(workspace) {
			root = filepath.Clean(root)
			if sameOrAncestor(candidate, root) || broadExpansionOf(candidate, root) || protectedHomeChild(candidate, root) {
				return true, true, root
			}
		}
	}
	return false, true, normalized
}

// canonicalRemovalTarget follows parent symlinks and glob prefixes, but not a
// final symlink: rm removes a final symlink itself, while `link/*` traverses the
// linked directory before rm receives its expanded arguments.
func canonicalRemovalTarget(path string) (string, bool) {
	if !containsGlob(path) {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", false
		}
		canonical, err := filepath.EvalSymlinks(path)
		return canonical, err == nil
	}
	firstGlob := strings.IndexAny(path, "*?[{")
	separator := strings.LastIndex(path[:firstGlob], string(filepath.Separator))
	if separator < 0 {
		return "", false
	}
	prefix := path[:separator]
	if prefix == "" {
		prefix = string(filepath.Separator)
	}
	canonical, err := filepath.EvalSymlinks(prefix)
	if err != nil {
		return "", false
	}
	return filepath.Join(canonical, path[separator+1:]), true
}

func protectedHomeChild(candidate, root string) bool {
	root = filepath.ToSlash(filepath.Clean(root))
	candidate = filepath.ToSlash(filepath.Clean(candidate))
	switch root {
	case "/home", "/Users":
		rel := strings.TrimPrefix(candidate, root+"/")
		return rel != candidate && rel != "" && !strings.Contains(rel, "/")
	default:
		return false
	}
}

func protectedRoots(cwd string) []string {
	roots := []string{string(filepath.Separator)}
	if cwd != "" {
		if abs, err := filepath.Abs(cwd); err == nil {
			roots = append(roots, abs, filepath.Join(abs, ".git"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		global := filepath.Join(home, ".collomia")
		roots = append(roots, home, global, filepath.Join(global, "sessions"), filepath.Join(global, "audit"), filepath.Join(global, "skills"))
	}
	for _, root := range []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/var", "/opt", "/srv", "/mnt", "/media", "/home", "/root", "/System", "/Library", "/Applications", "/Users", "/Volumes", "/var/root"} {
		roots = append(roots, filepath.FromSlash(root))
	}
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Fields(line)
				if len(fields) > 4 {
					roots = append(roots, unescapeMount(fields[4]))
				}
			}
		}
	}
	if runtime.GOOS == "darwin" {
		if entries, err := os.ReadDir("/Volumes"); err == nil {
			for _, entry := range entries {
				roots = append(roots, filepath.Join("/Volumes", entry.Name()))
			}
		}
	}
	return roots
}

func unescapeMount(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}

func sameOrAncestor(candidate, protected string) bool {
	if samePathText(candidate, protected) {
		return true
	}
	if containsGlob(candidate) {
		return false
	}
	rel, err := filepath.Rel(candidate, protected)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func broadExpansionOf(candidate, root string) bool {
	candidate = filepath.ToSlash(candidate)
	root = strings.TrimSuffix(filepath.ToSlash(root), "/")
	prefix := root + "/"
	if root == "" {
		prefix = "/"
	}
	if !strings.HasPrefix(candidate, prefix) {
		return false
	}
	rel := strings.TrimPrefix(candidate, prefix)
	if strings.Contains(rel, "/") {
		return false
	}
	switch rel {
	case "*", "**", ".*", ".??*", "{*,.*}", "{.*,*}":
		return true
	default:
		return false
	}
}

func isCriticalWriteTarget(target, cwd, workspace string) bool {
	resolved, ok := resolveStaticPath(target, cwd)
	if !ok {
		return false
	}
	clean := filepath.Clean(resolved)
	if workspace != "" {
		gitState := filepath.Join(workspace, ".git")
		for _, protected := range []string{
			filepath.Join(gitState, "HEAD"), filepath.Join(gitState, "index"), filepath.Join(gitState, "config"), filepath.Join(gitState, "packed-refs"),
		} {
			if samePathText(clean, protected) {
				return true
			}
		}
		for _, protectedTree := range []string{filepath.Join(gitState, "objects"), filepath.Join(gitState, "refs"), filepath.Join(gitState, "logs")} {
			if insideOrSame(protectedTree, clean) {
				return true
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		global := filepath.Join(home, ".collomia")
		for _, protected := range []string{
			filepath.Join(global, "config.json"), filepath.Join(global, "trust.json"), filepath.Join(global, "mcp-pins.json"),
		} {
			if samePathText(clean, protected) {
				return true
			}
		}
	}
	for _, protected := range []string{"/etc/passwd", "/etc/shadow", "/etc/sudoers", "/proc/sysrq-trigger", "/dev/mem", "/dev/kmem"} {
		if samePathText(clean, filepath.FromSlash(protected)) {
			return true
		}
	}
	return false
}

func resolveStaticPath(target, cwd string) (string, bool) {
	expanded, ok := expandStaticTarget(target)
	if !ok {
		return "", false
	}

	// Keep Unix system paths recognizable even when the host uses different
	// path semantics. Windows can still invoke commands through MSYS, Git Bash,
	// WSL, or another nested shell, so /dev, /proc, and /sys targets must reach
	// the catastrophic-command classifier instead of being joined to cwd.
	portable := strings.ReplaceAll(expanded, `\`, "/")
	if isPortableSystemPath(portable) {
		return filepath.Clean(filepath.FromSlash(portable)), true
	}

	if containsGlob(expanded) {
		if filepath.IsAbs(expanded) {
			return filepath.Clean(expanded), true
		}
		if cwd != "" {
			return filepath.Clean(filepath.Join(cwd, expanded)), true
		}
		return "", false
	}
	if isWindowsDriveRoot(expanded) || strings.HasPrefix(expanded, `\\`) {
		return expanded, true
	}
	if !filepath.IsAbs(expanded) {
		if cwd == "" {
			return "", false
		}
		expanded = filepath.Join(cwd, expanded)
	}
	return filepath.Clean(expanded), true
}

func isPortableSystemPath(path string) bool {
	lower := strings.ToLower(path)
	for _, root := range []string{"/dev", "/proc", "/sys"} {
		if lower == root || strings.HasPrefix(lower, root+"/") {
			return true
		}
	}
	return false
}

func expandStaticTarget(target string) (string, bool) {
	target = strings.TrimSpace(strings.Trim(target, `"'`))
	if target == "" {
		return "", false
	}
	if strings.HasPrefix(target, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		if target == "~" {
			target = home
		} else if strings.HasPrefix(target, "~/") || strings.HasPrefix(target, `~\`) {
			target = filepath.Join(home, target[2:])
		} else {
			// Shells can expand ~other-user, but doing that correctly requires
			// account database lookup; force a one-time confirmation instead.
			return target, false
		}
	}
	if profile := os.Getenv("USERPROFILE"); profile != "" {
		target = replaceFold(target, "%USERPROFILE%", profile)
	}
	missing := false
	target = os.Expand(target, func(key string) string {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		missing = true
		return "$" + key
	})
	if missing || strings.Contains(target, "$") || strings.Contains(target, "`") || strings.Contains(strings.ToLower(target), "%userprofile%") {
		return target, false
	}
	return target, true
}

func replaceFold(value, old, replacement string) string {
	for {
		index := strings.Index(strings.ToLower(value), strings.ToLower(old))
		if index < 0 {
			return value
		}
		value = value[:index] + replacement + value[index+len(old):]
	}
}

func isRawDevice(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	for _, prefix := range []string{"/dev/sd", "/dev/hd", "/dev/vd", "/dev/xvd", "/dev/nvme", "/dev/mmcblk", "/dev/loop", "/dev/md", "/dev/dm-", "/dev/zvol/", "/dev/mapper/", "/dev/disk", "/dev/rdisk", `//./physicaldrive`, `//?/globalroot/device/harddisk`} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeDevice != 0 && info.Mode()&os.ModeCharDevice == 0 {
		return true
	}
	return lower == "/dev/mem" || lower == "/dev/kmem" || lower == "/proc/sysrq-trigger"
}

func isWindowsProtectedTarget(target string) bool {
	lower := strings.ToLower(strings.ReplaceAll(target, `\`, "/"))
	if isWindowsDriveRoot(lower) || windowsDriveGlobRE.MatchString(lower) {
		return true
	}
	volume := "c:"
	if len(lower) >= 2 && lower[1] == ':' {
		volume = lower[:2]
	}
	for _, suffix := range []string{"/windows", "/program files", "/program files (x86)", "/programdata", "/users"} {
		root := volume + suffix
		if lower == root || broadSlashExpansion(lower, root) {
			return true
		}
		if suffix == "/users" {
			rel := strings.TrimPrefix(lower, root+"/")
			parts := strings.Split(rel, "/")
			if rel != lower && rel != "" && (len(parts) == 1 || len(parts) == 2 && isBroadComponent(parts[1])) {
				return true
			}
		}
	}
	if strings.HasPrefix(lower, "//") {
		parts := strings.Split(strings.Trim(lower, "/"), "/")
		return len(parts) == 2 || len(parts) == 3 && isBroadComponent(parts[2])
	}
	return false
}

func broadSlashExpansion(candidate, root string) bool {
	if !strings.HasPrefix(candidate, root+"/") {
		return false
	}
	rel := strings.TrimPrefix(candidate, root+"/")
	return !strings.Contains(rel, "/") && isBroadComponent(rel)
}

func isBroadComponent(value string) bool {
	switch value {
	case "*", "**", ".*", ".??*", "{*,.*}", "{.*,*}":
		return true
	default:
		return false
	}
}

func isWindowsDriveRoot(value string) bool {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	return len(value) == 3 && value[1] == ':' && value[2] == '/' && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z'))
}

func isWindowsVolumeTarget(value string) bool {
	return windowsVolumeRE.MatchString(strings.Trim(strings.TrimSpace(value), `"'`))
}

func containsGlob(value string) bool { return strings.ContainsAny(value, "*?[{") }

func samePathText(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func insideOrSame(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if samePathText(root, candidate) {
		return true
	}
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func nonOptionArgs(args []string, optionPrefix string) []string {
	var out []string
	options := true
	for _, arg := range args {
		if options && arg == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(arg, optionPrefix) {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func likelyDeviceTargets(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-t" || arg == "--type" || arg == "-L" || arg == "--label" || arg == "-U" || arg == "--uuid" || arg == "-o" || arg == "--offset" {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func containsShortOrLongOption(args []string, short rune, long string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, long) {
			return true
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.ContainsRune(arg[1:], short) {
			return true
		}
	}
	return false
}

func containsAnyFold(values []string, candidates ...string) bool {
	for _, value := range values {
		for _, candidate := range candidates {
			if strings.EqualFold(value, candidate) {
				return true
			}
		}
	}
	return false
}
