//go:build windows

package sandbox

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsBackend uses Windows' built-in AppContainer security boundary for
// filesystem, credential, process, and network isolation. A Job Object owns
// the complete descendant tree. It needs no Hyper-V feature, administrator
// rights, service, driver, or separately installed runtime.
type windowsBackend struct{}

func platformBackend() Backend { return windowsBackend{} }

func (windowsBackend) Name() string { return "AppContainer + Job Object" }

func (windowsBackend) Capabilities() Capabilities {
	return Capabilities{
		WriteIsolation:      true,
		ReadIsolation:       true,
		ReadIsolationAlways: true,
		NetworkIsolation:    NetworkFull,
		ProcessIsolation:    true,
		Notes:               []string{"built into Windows 11; no optional feature required", "loopback to unpackaged local services remains blocked"},
	}
}

func (windowsBackend) Available() error {
	for _, proc := range []*windows.LazyProc{
		procCreateAppContainerProfile,
		procDeriveAppContainerSID,
		procNtCreateDirectoryObject,
		procNtCreateSymbolicLink,
	} {
		if err := proc.Find(); err != nil {
			return fmt.Errorf("required AppContainer API %s is unavailable: %w", proc.Name, err)
		}
	}
	return nil
}

func (b windowsBackend) Wrap(argv []string, policy Policy) ([]string, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	if err := b.Available(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(policy.WorkspaceRoot) == "" {
		return nil, errors.New("sandbox policy requires a workspace root")
	}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot locate collo executable for the AppContainer shim: %w", err)
	}
	encoded, err := EncodePolicy(policy)
	if err != nil {
		return nil, err
	}
	return append([]string{self, "__appcontainer", encoded, "--"}, argv...), nil
}

var (
	userenvDLL                    = windows.NewLazySystemDLL("userenv.dll")
	procCreateAppContainerProfile = userenvDLL.NewProc("CreateAppContainerProfile")
	procDeriveAppContainerSID     = userenvDLL.NewProc("DeriveAppContainerSidFromAppContainerName")
	ntdllDLL                      = windows.NewLazySystemDLL("ntdll.dll")
	procNtCreateDirectoryObject   = ntdllDLL.NewProc("NtCreateDirectoryObject")
	procNtCreateSymbolicLink      = ntdllDLL.NewProc("NtCreateSymbolicLinkObject")
)

const (
	hresultAlreadyExists                    = 0x800700b7
	procThreadAttributeSecurityCapabilities = 0x00020009
	directoryAllAccess                      = 0x000f000f
	symbolicLinkAllAccess                   = 0x000f0001
)

type securityCapabilities struct {
	AppContainerSID *windows.SID
	Capabilities    *windows.SIDAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

// RunAppContainer is used by the hidden Windows re-exec shim. It launches the
// requested argv inside an AppContainer, waits for it, and returns an error
// when setup fails or the command exits unsuccessfully.
func RunAppContainer(policy Policy, argv []string) error {
	if len(argv) == 0 {
		return errors.New("empty AppContainer command")
	}
	workspace, err := filepath.Abs(policy.WorkspaceRoot)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	if real, evalErr := filepath.EvalSymlinks(workspace); evalErr == nil {
		workspace = real
	}
	profileName := appContainerProfileName(workspace)
	appSID, err := ensureAppContainerProfile(profileName)
	if err != nil {
		return err
	}
	defer windows.FreeSid(appSID) // allocated by Userenv.dll

	writable := append([]string{workspace, os.TempDir()}, policy.ExtraWritableRoots...)
	seen := map[string]bool{}
	for _, root := range writable {
		abs, absErr := filepath.Abs(root)
		if absErr != nil {
			return fmt.Errorf("resolve writable root %q: %w", root, absErr)
		}
		key := strings.ToLower(filepath.Clean(abs))
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := grantAppContainerAccess(abs, appSID, windows.GENERIC_ALL); err != nil {
			return fmt.Errorf("grant AppContainer access to %s: %w", abs, err)
		}
	}
	for _, root := range policy.ExtraReadableRoots {
		abs, absErr := filepath.Abs(root)
		if absErr != nil {
			return fmt.Errorf("resolve readable root %q: %w", root, absErr)
		}
		if real, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
			abs = real
		}
		key := strings.ToLower(filepath.Clean(abs))
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := grantAppContainerAccess(abs, appSID, windows.GENERIC_READ|windows.GENERIC_EXECUTE); err != nil {
			return fmt.Errorf("grant AppContainer read access to %s: %w", abs, err)
		}
	}

	target, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("find sandboxed executable %q: %w", argv[0], err)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve sandboxed executable: %w", err)
	}
	// AppContainer cannot normally read user-local tool installations. Grant
	// this workspace-specific container read/execute access to PATH entries
	// under the user's profile; system and Program Files binaries already carry
	// the normal application-package ACLs and are left untouched.
	userHome, _ := os.UserHomeDir()
	readable := append(filepath.SplitList(os.Getenv("PATH")), filepath.Dir(target))
	seenReadable := map[string]bool{}
	for _, root := range readable {
		root = strings.Trim(strings.TrimSpace(root), `"`)
		if root == "" || strings.EqualFold(filepath.Clean(root), filepath.Clean(userHome)) || !pathWithin(root, userHome) || pathWithin(root, workspace) {
			continue
		}
		key := strings.ToLower(filepath.Clean(root))
		if seenReadable[key] {
			continue
		}
		seenReadable[key] = true
		if _, statErr := os.Stat(root); errors.Is(statErr, os.ErrNotExist) {
			continue
		} else if statErr != nil {
			return fmt.Errorf("inspect executable directory %s: %w", root, statErr)
		}
		if err := grantAppContainerAccess(root, appSID, windows.GENERIC_READ|windows.GENERIC_EXECUTE); err != nil {
			return fmt.Errorf("grant AppContainer read access to executable directory %s: %w", root, err)
		}
	}
	nullDevice, err := newAppContainerNullDevice(appSID)
	if err != nil {
		return err
	}
	defer nullDevice.Close()
	return createAppContainerProcess(appSID, target, argv, workspace, policy.AllowNetwork, nullDevice)
}

func pathWithin(path, root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	pathAbs, pathErr := filepath.Abs(path)
	rootAbs, rootErr := filepath.Abs(root)
	if pathErr != nil || rootErr != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func appContainerProfileName(workspace string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(workspace))))
	return fmt.Sprintf("Collomia.Sandbox.%x", sum[:12])
}

func ensureAppContainerProfile(name string) (*windows.SID, error) {
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	display16, _ := windows.UTF16PtrFromString("Collomia command sandbox")
	description16, _ := windows.UTF16PtrFromString("Per-workspace AppContainer used by Collomia")
	var sid *windows.SID
	hr, _, _ := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(name16)),
		uintptr(unsafe.Pointer(display16)),
		uintptr(unsafe.Pointer(description16)),
		0,
		0,
		uintptr(unsafe.Pointer(&sid)),
	)
	if uint32(hr) == hresultAlreadyExists {
		hr, _, _ = procDeriveAppContainerSID.Call(uintptr(unsafe.Pointer(name16)), uintptr(unsafe.Pointer(&sid)))
	}
	if int32(uint32(hr)) < 0 {
		return nil, fmt.Errorf("create or open AppContainer profile %q: HRESULT 0x%08x", name, uint32(hr))
	}
	if sid == nil || !sid.IsValid() {
		return nil, errors.New("Windows returned an invalid AppContainer SID")
	}
	return sid, nil
}

// grantAppContainerAccess merges one inheritable ACE into the existing DACL.
// The ACE names a workspace-specific AppContainer SID, not a user or broad
// group, and therefore grants no additional access to ordinary processes.
func grantAppContainerAccess(path string, sid *windows.SID, permissions windows.ACCESS_MASK) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	inheritance := uint32(0)
	if info.IsDir() {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entry := windows.EXPLICIT_ACCESS{
		AccessPermissions: permissions,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
	merged, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, dacl)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, merged, nil)
}

func createAppContainerProcess(appSID *windows.SID, target string, argv []string, workspace string, allowNetwork bool, nullDevice *appContainerNullDevice) error {
	var capabilitySIDs []*windows.SID
	if allowNetwork {
		internet, err := windows.CreateWellKnownSid(windows.WinCapabilityInternetClientSid)
		if err != nil {
			return fmt.Errorf("create internetClient capability: %w", err)
		}
		privateNetwork, err := windows.CreateWellKnownSid(windows.WinCapabilityPrivateNetworkClientServerSid)
		if err != nil {
			return fmt.Errorf("create privateNetworkClientServer capability: %w", err)
		}
		capabilitySIDs = append(capabilitySIDs, internet, privateNetwork)
	}
	attributes := make([]windows.SIDAndAttributes, len(capabilitySIDs))
	for i, sid := range capabilitySIDs {
		attributes[i] = windows.SIDAndAttributes{Sid: sid, Attributes: windows.SE_GROUP_ENABLED}
	}
	security := securityCapabilities{AppContainerSID: appSID, CapabilityCount: uint32(len(attributes))}
	if len(attributes) > 0 {
		security.Capabilities = &attributes[0]
	}
	attributeList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fmt.Errorf("create process attribute list: %w", err)
	}
	defer attributeList.Delete()
	if err := attributeList.Update(procThreadAttributeSecurityCapabilities, unsafe.Pointer(&security), unsafe.Sizeof(security)); err != nil {
		return fmt.Errorf("set AppContainer security capabilities: %w", err)
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create sandbox Job Object: %w", err)
	}
	defer windows.CloseHandle(job)
	jobInfo := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	jobInfo.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&jobInfo)), uint32(unsafe.Sizeof(jobInfo))); err != nil {
		return fmt.Errorf("configure sandbox Job Object: %w", err)
	}

	commandLine16, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	if err != nil {
		return err
	}
	target16, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	workspace16, err := windows.UTF16PtrFromString(workspace)
	if err != nil {
		return err
	}
	stdin, _ := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	stdout, _ := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	stderr, _ := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	startup := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:        uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags:     windows.STARTF_USESTDHANDLES,
			StdInput:  stdin,
			StdOutput: stdout,
			StdErr:    stderr,
		},
		ProcThreadAttributeList: attributeList.List(),
	}
	var process windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP)
	if err := windows.CreateProcess(target16, commandLine16, nil, nil, true, flags, nil, workspace16, &startup.StartupInfo, &process); err != nil {
		return fmt.Errorf("launch AppContainer process: %w", err)
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)
	if err := nullDevice.Install(process.Process); err != nil {
		_ = windows.TerminateProcess(process.Process, 1)
		return err
	}
	if err := windows.AssignProcessToJobObject(job, process.Process); err != nil {
		_ = windows.TerminateProcess(process.Process, 1)
		return fmt.Errorf("assign AppContainer process to Job Object: %w", err)
	}
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		_ = windows.TerminateProcess(process.Process, 1)
		return fmt.Errorf("resume AppContainer process: %w", err)
	}
	if _, err := windows.WaitForSingleObject(process.Process, windows.INFINITE); err != nil {
		return fmt.Errorf("wait for AppContainer process: %w", err)
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(process.Process, &exitCode); err != nil {
		return fmt.Errorf("read AppContainer exit status: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("sandboxed command exited with status %d", exitCode)
	}
	return nil
}

// AppContainers cannot open Windows' kernel NUL device. That breaks common
// unmodified developer tools: Go's os/exec, for example, opens NUL whenever a
// child command has no stdin. A process-local DOS device map lets this one
// sandboxed process tree resolve NUL to an empty AppContainer-accessible file
// while all other DOS devices continue to fall back to Windows' global map.
// No machine-wide device ACL is changed and no preexisting host path is exposed.
type appContainerNullDevice struct {
	directory windows.Handle
	link      windows.Handle
	path      string
}

func newAppContainerNullDevice(appSID *windows.SID) (*appContainerNullDevice, error) {
	file, err := os.CreateTemp("", "collomia-appcontainer-null-*")
	if err != nil {
		return nil, fmt.Errorf("create AppContainer null target: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close AppContainer null target: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(path)
	}
	if err := grantAppContainerAccess(path, appSID, windows.GENERIC_READ|windows.GENERIC_WRITE); err != nil {
		cleanup()
		return nil, fmt.Errorf("grant AppContainer access to null target: %w", err)
	}

	sum := sha256.Sum256([]byte(strings.ToLower(path)))
	directoryName, err := windows.NewNTUnicodeString(fmt.Sprintf(`\??\Collomia.Sandbox.DeviceMap.%d.%x`, os.Getpid(), sum[:8]))
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("encode AppContainer device-map name: %w", err)
	}
	directoryAttributes := windows.OBJECT_ATTRIBUTES{
		Length:     uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		ObjectName: directoryName,
		Attributes: windows.OBJ_CASE_INSENSITIVE,
	}
	var directory windows.Handle
	status, _, _ := procNtCreateDirectoryObject.Call(
		uintptr(unsafe.Pointer(&directory)),
		directoryAllAccess,
		uintptr(unsafe.Pointer(&directoryAttributes)),
	)
	if status != 0 {
		cleanup()
		return nil, fmt.Errorf("create AppContainer process device map: %w", windows.NTStatus(status))
	}

	linkName, err := windows.NewNTUnicodeString("NUL")
	if err != nil {
		_ = windows.CloseHandle(directory)
		cleanup()
		return nil, fmt.Errorf("encode AppContainer null-device name: %w", err)
	}
	targetName, err := windows.NewNTUnicodeString(globalDOSPath(path))
	if err != nil {
		_ = windows.CloseHandle(directory)
		cleanup()
		return nil, fmt.Errorf("encode AppContainer null-device target: %w", err)
	}
	linkAttributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: directory,
		ObjectName:    linkName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	var link windows.Handle
	status, _, _ = procNtCreateSymbolicLink.Call(
		uintptr(unsafe.Pointer(&link)),
		symbolicLinkAllAccess,
		uintptr(unsafe.Pointer(&linkAttributes)),
		uintptr(unsafe.Pointer(targetName)),
	)
	if status != 0 {
		_ = windows.CloseHandle(directory)
		cleanup()
		return nil, fmt.Errorf("create AppContainer null-device link: %w", windows.NTStatus(status))
	}
	return &appContainerNullDevice{directory: directory, link: link, path: path}, nil
}

func (d *appContainerNullDevice) Install(process windows.Handle) error {
	if d == nil || d.directory == 0 {
		return errors.New("AppContainer null-device map is unavailable")
	}
	// PROCESS_DEVICEMAP_INFORMATION is a union. The set form uses only the
	// leading handle; the remaining bytes retain the native query-form size.
	var information struct {
		Directory windows.Handle
		QueryData [32]byte
	}
	information.Directory = d.directory
	if err := windows.NtSetInformationProcess(
		process,
		int32(windows.ProcessDeviceMap),
		unsafe.Pointer(&information),
		uint32(unsafe.Sizeof(information)),
	); err != nil {
		return fmt.Errorf("install AppContainer process device map: %w", err)
	}
	return nil
}

func (d *appContainerNullDevice) Close() {
	if d == nil {
		return
	}
	if d.link != 0 {
		_ = windows.CloseHandle(d.link)
		d.link = 0
	}
	if d.directory != 0 {
		_ = windows.CloseHandle(d.directory)
		d.directory = 0
	}
	if d.path != "" {
		_ = os.Remove(d.path)
		d.path = ""
	}
}

func globalDOSPath(path string) string {
	clean := filepath.Clean(path)
	if strings.HasPrefix(clean, `\\?\`) {
		clean = strings.TrimPrefix(clean, `\\?\`)
	}
	if strings.HasPrefix(clean, `\\`) {
		return `\GLOBAL??\UNC\` + strings.TrimPrefix(clean, `\\`)
	}
	return `\GLOBAL??\` + clean
}
