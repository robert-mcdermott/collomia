//go:build windows

package credstore

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// backendName is what the user is told holds their credential.
const backendName = "Windows Credential Manager"

// targetPrefix namespaces Collomia's generic credentials so they are
// recognizable in the Credential Manager control panel and cannot collide
// with another application's target names.
const targetPrefix = "collomia:"

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
	// maxBlobBytes is CRED_MAX_CREDENTIAL_BLOB_SIZE. API keys are far below
	// it; the check exists so an oversized value fails with an explanation
	// rather than an opaque Windows error code.
	maxBlobBytes = 5 * 512
)

// credentialW mirrors the Win32 CREDENTIALW structure. Go's field alignment
// matches the C layout for these types on the supported architectures.
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

func backendGet(account string) (string, bool, error) {
	target, err := windows.UTF16PtrFromString(targetPrefix + account)
	if err != nil {
		return "", false, err
	}
	var credential *credentialW
	ret, _, callErr := procCredReadW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0,
		uintptr(unsafe.Pointer(&credential)))
	if ret == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("could not read the Credential Manager entry for %q: %w", account, callErr)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential.CredentialBlobSize == 0 || credential.CredentialBlob == nil {
		return "", true, nil
	}
	blob := unsafe.Slice(credential.CredentialBlob, credential.CredentialBlobSize)
	return string(append([]byte(nil), blob...)), true, nil
}

func backendSet(account, secret string) error {
	if len(secret) > maxBlobBytes {
		return fmt.Errorf("credential is %d bytes; the Windows Credential Manager accepts at most %d", len(secret), maxBlobBytes)
	}
	target, err := windows.UTF16PtrFromString(targetPrefix + account)
	if err != nil {
		return err
	}
	comment, err := windows.UTF16PtrFromString("Collomia provider credential")
	if err != nil {
		return err
	}
	user, err := windows.UTF16PtrFromString(account)
	if err != nil {
		return err
	}
	blob := []byte(secret)
	credential := credentialW{
		Type:               credTypeGeneric,
		TargetName:         target,
		Comment:            comment,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocalMachine,
		UserName:           user,
	}
	ret, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&credential)), 0)
	if ret == 0 {
		return fmt.Errorf("could not store the Credential Manager entry for %q: %w", account, callErr)
	}
	return nil
}

func backendDelete(account string) (bool, error) {
	target, err := windows.UTF16PtrFromString(targetPrefix + account)
	if err != nil {
		return false, err
	}
	ret, _, callErr := procCredDeleteW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0)
	if ret == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return false, nil
		}
		return false, fmt.Errorf("could not delete the Credential Manager entry for %q: %w", account, callErr)
	}
	return true, nil
}
