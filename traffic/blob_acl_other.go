//go:build !windows

package traffic

import "os"

// Unix capture roots are mode 0700. A private parent protects every legacy
// descendant and stage files are created with mode 0600.
func secureCaptureDirectory(string) error    { return nil }
func secureExistingCaptureTree(string) error { return nil }
func secureCaptureStage(*os.File) error      { return nil }
