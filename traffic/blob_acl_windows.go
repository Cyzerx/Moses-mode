//go:build windows

package traffic

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows access checks use a DACL. Restrict the owner as well since an
// object's owner can rewrite its DACL. Administrators can still take ownership.
type captureACL struct {
	owner     *windows.SID
	file, dir *windows.SECURITY_DESCRIPTOR
}

func newCaptureACL() (*captureACL, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sid := u.User.Sid.String()
	file, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;FA;;;" + sid + ")")
	if err != nil {
		return nil, err
	}
	dir, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;OICI;FA;;;" + sid + ")")
	if err != nil {
		return nil, err
	}
	return &captureACL{owner: u.User.Sid, file: file, dir: dir}, nil
}

// Pin the final entry while inspecting its ACL and (for a directory) visiting
// children. Opening reparse points themselves and refusing them prevents a
// legacy junction or symlink from changing permissions outside the data store.
func openCaptureSecurityHandle(path string, access uint32) (windows.Handle, error) {
	// CreateFileW does not provide os.Open's automatic long-path conversion.
	if !strings.HasPrefix(path, `\\?\`) {
		if strings.HasPrefix(path, `\\`) {
			path = `\\?\UNC\` + path[2:]
		} else {
			path = `\\?\` + path
		}
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(name, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
}

const captureSecurityInfo = windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION

func (a *captureACL) protected(sd *windows.SECURITY_DESCRIPTOR, directory bool) bool {
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !a.owner.Equals(owner) {
		return false
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 1 {
		return false
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if windows.GetAce(acl, 0, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return false
	}
	var flags uint8
	if directory {
		flags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	// FILE_ALL_ACCESS is STANDARD_RIGHTS_REQUIRED | SYNCHRONIZE | 0x1ff.
	const fullAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
	return ace.Mask == fullAccess && ace.Header.AceFlags == flags &&
		a.owner.Equals((*windows.SID)(unsafe.Pointer(&ace.SidStart)))
}

func (a *captureACL) secure(path string, recursive bool, stage *os.File) error {
	entry, err := os.Lstat(path)
	if err != nil {
		return err
	}
	access := uint32(windows.READ_CONTROL | windows.WRITE_DAC | windows.FILE_READ_ATTRIBUTES)
	if entry.IsDir() {
		// Microsoft's SetSecurityInfo contract excludes recursive ACL propagation
		// for MAXIMUM_ALLOWED handles. Visit children ourselves after checking
		// their type and link count, so external hard links are never changed.
		// https://learn.microsoft.com/windows/win32/api/aclapi/nf-aclapi-setsecurityinfo#remarks
		access = windows.MAXIMUM_ALLOWED
	}
	h, err := openCaptureSecurityHandle(path, access)
	if err != nil {
		return err
	}
	pinned := os.NewFile(uintptr(h), path)
	defer pinned.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("capture storage contains a reparse point")
	}
	directory := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if directory != entry.IsDir() {
		return errors.New("capture entry changed during ACL validation")
	}
	if !directory && info.NumberOfLinks != 1 {
		return errors.New("copy multiply linked capture files to private individual files before starting Artex-Moses-mode")
	}
	if stage != nil {
		var open windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(windows.Handle(stage.Fd()), &open); err != nil {
			return err
		}
		if open.VolumeSerialNumber != info.VolumeSerialNumber || open.FileIndexHigh != info.FileIndexHigh || open.FileIndexLow != info.FileIndexLow {
			return errors.New("capture staging file changed before its ACL could be applied")
		}
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, captureSecurityInfo)
	if err != nil {
		return err
	}
	if !a.protected(sd, directory) {
		owner, _, err := sd.Owner()
		if err != nil {
			return err
		}
		target := h
		flags := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
		var newOwner *windows.SID
		if owner == nil || !a.owner.Equals(owner) {
			if !directory {
				target, err = openCaptureSecurityHandle(path, access|windows.WRITE_OWNER)
				if err != nil {
					return fmt.Errorf("capture owner must be the Artex-Moses-mode service account: %w", err)
				}
				defer windows.CloseHandle(target)
			}
			flags |= windows.OWNER_SECURITY_INFORMATION
			newOwner = a.owner
		}
		desired := a.file
		if directory {
			desired = a.dir
		}
		acl, _, err := desired.DACL()
		if err != nil || acl == nil {
			return errors.New("invalid private capture DACL")
		}
		if err := windows.SetSecurityInfo(target, windows.SE_FILE_OBJECT, flags, newOwner, nil, acl, nil); err != nil {
			return err
		}
		sd, err = windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, captureSecurityInfo)
		if err != nil {
			return err
		}
		if !a.protected(sd, directory) {
			return errors.New("capture filesystem did not enforce an owner-only DACL")
		}
	}
	if recursive && directory {
		for {
			children, readErr := pinned.ReadDir(128)
			for _, child := range children {
				if err := a.secure(filepath.Join(path, child.Name()), true, nil); err != nil {
					return fmt.Errorf("%s: %w", child.Name(), err)
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	return nil
}

func secureCaptureDirectory(path string) error    { return restrictCapture(path, false, nil) }
func secureExistingCaptureTree(path string) error { return restrictCapture(path, true, nil) }
func secureCaptureStage(f *os.File) error         { return restrictCapture(f.Name(), false, f) }

func restrictCapture(path string, recursive bool, stage *os.File) error {
	a, err := newCaptureACL()
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := a.secure(abs, recursive, stage); err != nil {
		return fmt.Errorf("secure capture storage %s: %w", abs, err)
	}
	return nil
}
