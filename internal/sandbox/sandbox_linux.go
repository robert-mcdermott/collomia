//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

// linuxBackend enforces policies with Landlock (kernel 5.13+). Because
// Landlock restricts the calling process, the command is re-executed through
// a hidden `collo __landlock` shim that applies the ruleset to itself and
// then execs the real command. Filesystem writes are confined to the granted
// roots; with ABI v4+ (kernel 6.7+) TCP connect/bind are also denied unless
// the policy allows network. UDP (including DNS) cannot be restricted by
// Landlock yet — doctor and SECURITY.md state this.
type linuxBackend struct{}

func platformBackend() Backend { return linuxBackend{} }

func (linuxBackend) Name() string {
	abi := landlockABI()
	if abi >= 4 {
		return fmt.Sprintf("Landlock (ABI v%d, fs+tcp)", abi)
	}
	return fmt.Sprintf("Landlock (ABI v%d, fs only)", abi)
}

func (linuxBackend) Capabilities() Capabilities {
	abi := landlockABI()
	caps := Capabilities{WriteIsolation: abi >= 1, Notes: []string{"process-group termination is best effort"}}
	if abi >= 4 {
		caps.NetworkIsolation = NetworkTCP
		caps.Notes = append(caps.Notes, "UDP is not confined by Landlock")
	}
	return caps
}

func (linuxBackend) Available() error {
	if abi := landlockABI(); abi < 1 {
		return fmt.Errorf("Landlock is unavailable (kernel too old or disabled)")
	}
	return nil
}

func (b linuxBackend) Wrap(argv []string, policy Policy) ([]string, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	if err := b.Available(); err != nil {
		return nil, err
	}
	if policy.WorkspaceRoot == "" {
		return nil, fmt.Errorf("sandbox policy requires a workspace root")
	}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot locate collo executable for the landlock shim: %w", err)
	}
	encoded, err := EncodePolicy(policy)
	if err != nil {
		return nil, err
	}
	return append([]string{self, "__landlock", encoded, "--"}, argv...), nil
}

func landlockABI() int {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(abi)
}

// ApplyLandlock restricts the current process according to the policy. It
// must be called by the shim before exec-ing the target command; it is
// irreversible for the process.
func ApplyLandlock(policy Policy) error {
	abi := landlockABI()
	if abi < 1 {
		return fmt.Errorf("Landlock unavailable")
	}
	writeAccess := uint64(unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		writeAccess |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		writeAccess |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	attr := unix.LandlockRulesetAttr{Access_fs: writeAccess}
	if abi >= 4 && !policy.AllowNetwork {
		attr.Access_net = unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP
	}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %w", errno)
	}
	defer unix.Close(int(fd))

	writable := append([]string{policy.WorkspaceRoot, os.TempDir(), "/tmp", "/dev/null", "/dev/tty", "/dev/shm", "/proc/self"}, policy.ExtraWritableRoots...)
	for _, root := range writable {
		if err := allowWrites(int(fd), root, writeAccess); err != nil {
			// Missing optional roots (e.g. /dev/shm) are fine; the workspace
			// itself is not.
			if root == policy.WorkspaceRoot {
				return err
			}
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl(NO_NEW_PRIVS): %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		return fmt.Errorf("landlock_restrict_self: %w", errno)
	}
	return nil
}

func allowWrites(rulesetFd int, root string, access uint64) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	parent, err := unix.Open(abs, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", abs, err)
	}
	defer unix.Close(parent)
	rule := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(parent)}
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(rulesetFd), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("landlock_add_rule %s: %w", abs, errno)
	}
	return nil
}
