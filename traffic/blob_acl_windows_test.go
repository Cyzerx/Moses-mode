//go:build windows

package traffic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func fixtureSecurity(t *testing.T, path string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, captureSecurityInfo)
	if err != nil {
		t.Fatal(err)
	}
	return sd
}

func TestWindowsCaptureACLProtectsOldAndStagedFiles(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "legacy")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(child, "capture.bin")
	if err := os.WriteFile(path, []byte("synthetic"), 0644); err != nil {
		t.Fatal(err)
	}
	a, err := newCaptureACL()
	if err != nil {
		t.Fatal(err)
	}
	// Protected children with an explicit Everyone-read ACE cannot be secured
	// merely by resetting the parent directory's inheritance.
	desc, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + a.owner.String() + ")(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := desc.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if a.protected(fixtureSecurity(t, path), false) {
		t.Fatal("fixture retained an owner-only ACL before migration")
	}
	if err := secureExistingCaptureTree(root); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{root, child, path} {
		if !a.protected(fixtureSecurity(t, p), p != path) {
			t.Fatalf("incorrect owner or DACL after migration: %s", p)
		}
	}
	stage, err := os.CreateTemp(child, ".stage-")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	// New files inherit only the account ACE even before the file is made
	// independently protected. WD is the SDDL identifier for Everyone.
	if strings.Contains(fixtureSecurity(t, stage.Name()).String(), ";;;WD)") {
		t.Fatal("new file inherited a public ACE")
	}
	if err := secureCaptureStage(stage); err != nil {
		t.Fatal(err)
	}
	if !a.protected(fixtureSecurity(t, stage.Name()), false) {
		t.Fatal("staging file has an inheritable or nonprivate ACL")
	}
}

func TestWindowsCaptureACLRefusesExternalHardlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(root, "alias")); err != nil {
		t.Skipf("test filesystem cannot create a hard link: %v", err)
	}
	before := fixtureSecurity(t, outside).String()
	if err := secureExistingCaptureTree(root); err == nil {
		t.Fatal("migration accepted a multiply linked file")
	}
	if after := fixtureSecurity(t, outside).String(); after != before {
		t.Fatal("migration changed an external hard link's permissions")
	}
}

func TestWindowsCaptureACLRefusesReparsePoints(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "external")); err != nil {
		t.Skipf("Windows symlink privilege unavailable: %v", err)
	}
	before := fixtureSecurity(t, outside).String()
	if err := secureExistingCaptureTree(root); err == nil {
		t.Fatal("migration followed a directory reparse point")
	}
	if after := fixtureSecurity(t, outside).String(); after != before {
		t.Fatal("migration changed the external directory")
	}
}
