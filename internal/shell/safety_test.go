package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatastrophicCommandsAreHardDenied(t *testing.T) {
	workspace := t.TempDir()
	cases := []string{
		"rm -rf /",
		"rm -fr /*",
		"rm -r -f /",
		"rm --recursive --force /",
		"rm --no-preserve-root -rf /tmp/example",
		"sudo rm -rf /",
		"env FOO=bar rm -rf /",
		"command rm -rf /",
		"sh -c 'rm -rf /*'",
		"rm -rf .",
		"rm -rf .git",
		"rm -rf .git/objects",
		"rm .git/HEAD",
		"cp /dev/null .git/HEAD",
		"truncate -s 0 .git/HEAD",
		"find . -delete",
		"chmod -R 000 /",
		"dd if=image.raw of=/dev/sda",
		"printf x > /dev/sda",
		"tee /proc/sysrq-trigger",
		"mkfs.ext4 /dev/nvme0n1",
		"parted /dev/sda mklabel gpt",
		`Remove-Item -Recurse -Force C:\`,
		`del /s /q C:\`,
		`powershell -Command "Remove-Item -Recurse -Force C:\Windows"`,
		`Remove-Item -Recurse -Force D:\Windows`,
		`Remove-Item -Recurse -Force "C:\Program Files"`,
		`Remove-Item -Recurse -Force C:\Users\someone\*`,
		`format \\.\PhysicalDrive0`,
		`format C:`,
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			a := AnalyzeInWorkspace(command, workspace)
			if len(a.HardDenyReasons) == 0 {
				t.Fatalf("expected hard denial; analysis=%+v", a)
			}
		})
	}
}

func TestRemovalSymlinkSemantics(t *testing.T) {
	workspace := t.TempDir()
	link := filepath.Join(workspace, "workspace-link")
	if err := os.Symlink(workspace, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if a := AnalyzeInWorkspace("rm -rf workspace-link", workspace); len(a.HardDenyReasons) > 0 {
		t.Fatalf("removing a final directory symlink does not traverse it: %v", a.HardDenyReasons)
	}
	if a := AnalyzeInWorkspace("rm -rf workspace-link/*", workspace); len(a.HardDenyReasons) == 0 {
		t.Fatalf("glob through a symlink to the workspace must be denied: %+v", a)
	}
}

func TestDirectoryChangeSafetyTracking(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		command string
		hard    bool
		confirm bool
	}{
		{"cd build && rm -rf *", false, false},
		{"cd missing || rm -rf *", true, false},
		{"cd build; rm -rf *", false, true},
		{"(cd build && rm -rf *); rm -rf *", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			a := AnalyzeInWorkspace(tc.command, workspace)
			if got := len(a.HardDenyReasons) > 0; got != tc.hard {
				t.Fatalf("hard=%v want %v; analysis=%+v", got, tc.hard, a)
			}
			if got := len(a.ConfirmReasons) > 0; got != tc.confirm {
				t.Fatalf("confirm=%v want %v; analysis=%+v", got, tc.confirm, a)
			}
		})
	}
}

func TestLegitimateScopedCommandsAreNotCatastrophic(t *testing.T) {
	workspace := t.TempDir()
	cases := []string{
		"rm -r directory",
		"rm -rf /tmp/example",
		"rm -rf node_modules",
		"mkdir -p build && cd build && rm -rf *",
		"mkdir -p build && cd build && sh -c 'rm -rf *'",
		"git clean -n",
		"git clean --dry-run",
		"mkfs.ext4 ./disk.img",
		"dd if=/dev/zero of=./disk.img count=1",
		"tee /dev/null",
		"rm -f .git/index.lock",
		"rm -f .collomia.json",
		`Remove-Item -Recurse .\node_modules`,
		`Remove-Item -Recurse "C:\Program Files\Example App"`,
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			a := AnalyzeInWorkspace(command, workspace)
			if len(a.HardDenyReasons) > 0 {
				t.Fatalf("unexpected hard denial: %s", strings.Join(a.HardDenyReasons, "; "))
			}
			if len(a.ConfirmReasons) > 0 {
				t.Fatalf("unexpected mandatory confirmation: %s", strings.Join(a.ConfirmReasons, "; "))
			}
		})
	}
}

func TestDestructiveButLegitimateCommandsRequireOneTimeConfirmation(t *testing.T) {
	workspace := t.TempDir()
	cases := []string{
		"git reset --hard",
		"git -C repo clean -fd",
		"git push --force-with-lease origin main",
		"git push -fu origin main",
		"git push origin +main:main",
		"git stash clear",
		"git switch --discard-changes feature",
		"shutdown now",
		"systemctl reboot",
		"terraform destroy",
		"kubectl delete namespace production",
		"kubectl --context production delete namespace production",
		"aws s3 rm s3://bucket --recursive",
		"aws --profile production s3 rm s3://bucket --recursive",
		"az group delete --name production --yes",
		"az --subscription production group delete --name production --yes",
		"gcloud projects delete production",
		"gcloud --project production compute instances delete web-1",
		"rm -rf $UNKNOWN_TARGET",
		"fdisk /dev/sda",
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			a := AnalyzeInWorkspace(command, workspace)
			if len(a.HardDenyReasons) > 0 {
				t.Fatalf("expected confirmation, got hard denial: %v", a.HardDenyReasons)
			}
			if len(a.ConfirmReasons) == 0 {
				t.Fatalf("expected mandatory confirmation; analysis=%+v", a)
			}
		})
	}
}
