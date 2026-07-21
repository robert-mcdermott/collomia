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
// roots. Read confinement can additionally restrict user-data reads to the
// workspace, system runtime paths, PATH entries, and explicit grants. With
// ABI v4+ (kernel 6.7+) TCP connect/bind are also denied unless the policy
// allows network; ABI v10+ additionally covers UDP bind/connect/send, including
// DNS. Capability reporting keeps older kernels explicitly TCP-only.
type linuxBackend struct{}

func platformBackend() Backend { return linuxBackend{} }

func (linuxBackend) Name() string {
	abi := landlockABI()
	if abi >= 10 {
		return fmt.Sprintf("Landlock (ABI v%d, fs+tcp+udp)", abi)
	}
	if abi >= 4 {
		return fmt.Sprintf("Landlock (ABI v%d, fs+tcp)", abi)
	}
	return fmt.Sprintf("Landlock (ABI v%d, fs only)", abi)
}

func (linuxBackend) Capabilities() Capabilities {
	abi := landlockABI()
	caps := Capabilities{WriteIsolation: abi >= 1, ReadIsolation: abi >= 1, Notes: []string{"process-group termination is best effort"}}
	caps.NetworkIsolation = landlockNetworkIsolation(abi)
	if caps.NetworkIsolation == NetworkTCP {
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
	readAccess := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
	handledAccess := writeAccess
	if policy.ConstrainReads {
		handledAccess |= readAccess
	}
	attr := unix.LandlockRulesetAttr{Access_fs: handledAccess}
	attr.Access_net = landlockHandledNetwork(abi, policy.AllowNetwork)
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %w", errno)
	}
	defer unix.Close(int(fd))

	writable := append([]string{policy.WorkspaceRoot, os.TempDir(), "/tmp", "/dev/null", "/dev/tty", "/dev/shm", "/proc/self"}, policy.ExtraWritableRoots...)
	writableAccess := writeAccess
	if policy.ConstrainReads {
		writableAccess |= readAccess
	}
	for _, root := range writable {
		if err := allowPath(int(fd), root, writableAccess); err != nil {
			// Missing optional roots (e.g. /dev/shm) are fine; the workspace
			// itself is not.
			if root == policy.WorkspaceRoot {
				return err
			}
		}
	}
	if policy.ConstrainReads {
		readable := append([]string{}, linuxSystemReadableRoots()...)
		readable = append(readable, filepath.SplitList(os.Getenv("PATH"))...)
		for _, root := range readable {
			// System and PATH roots are compatibility grants. A missing optional
			// runtime is harmless; an explicitly configured root is validated by
			// configuration but may still disappear between load and execution.
			_ = allowPath(int(fd), root, readAccess)
		}
		for _, root := range policy.ExtraReadableRoots {
			if err := allowPath(int(fd), root, readAccess); err != nil {
				return fmt.Errorf("grant readable root %s: %w", root, err)
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

// Landlock ABI v10 added UDP rights after the Linux headers used to generate
// the pinned x/sys version. The ruleset struct already exposes Access_net, and
// these stable UAPI values come directly from linux/landlock.h. Keeping them
// local lets one binary use the newer kernel capability without dropping
// compatibility with older kernels, where the bits are never sent.
const (
	landlockAccessNetBindUDP        = uint64(1 << 2)
	landlockAccessNetConnectSendUDP = uint64(1 << 3)
)

func landlockNetworkIsolation(abi int) NetworkIsolation {
	switch {
	case abi >= 10:
		return NetworkFull
	case abi >= 4:
		return NetworkTCP
	default:
		return NetworkNone
	}
}

func landlockHandledNetwork(abi int, allowNetwork bool) uint64 {
	if allowNetwork || abi < 4 {
		return 0
	}
	access := uint64(unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP)
	if abi >= 10 {
		access |= landlockAccessNetBindUDP | landlockAccessNetConnectSendUDP
	}
	return access
}

func allowPath(rulesetFd int, root string, access uint64) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", abs, err)
	}
	if !info.IsDir() {
		// Directory-entry creation/removal and READ_DIR do not apply to regular
		// files and make LANDLOCK_ADD_RULE reject the otherwise useful rule.
		fileAccess := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE)
		if landlockABI() >= 3 {
			fileAccess |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
		}
		access &= fileAccess
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

func linuxSystemReadableRoots() []string {
	// Keep system runtimes and trust/config data usable while excluding user
	// homes. Filesystem read confinement is aimed at ungranted user data; it is
	// not a claim that public operating-system configuration becomes invisible.
	return []string{
		"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt", "/nix",
		"/snap", "/dev", "/proc/self", "/proc/thread-self",
	}
}
