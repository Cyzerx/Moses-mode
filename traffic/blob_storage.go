package traffic

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// Restrict existing capture roots as well as newly created ones. Protecting the
// common parent immediately covers legacy blobs, SQLite journals and CA files.
func privateTrafficDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("capture directory is not a directory: %s", path)
	}
	if runtime.GOOS == "windows" {
		return secureCaptureDirectory(path)
	}
	if info.Mode().Perm() != 0700 {
		return os.Chmod(path, 0700)
	}
	return nil
}

var errBlobChecksum = errors.New("capture blob checksum mismatch")

func openVerifiedBlob(root *os.Root, name, hash string) (*os.File, int64, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, errors.New("invalid capture blob file type")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, 0, err
	}
	digest := sha256.New()
	n, err := io.Copy(digest, f)
	if err == nil && hex.EncodeToString(digest.Sum(nil)) != hash {
		err = errBlobChecksum
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, n, nil
}

// publishBlob is called with a digest computed or verified by its caller.
// Replacing a damaged legacy blob is safe because all writers publish the same
// content for a given hash and the final name appears only after a complete write.
func (t *Traffic) publishBlob(hash string, data []byte) error {
	root, err := os.OpenRoot(t.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	dir := filepath.Join("_blobs", "sha256", hash[:2])
	if err := root.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := root.Chmod(dir, 0700); err != nil {
		return err
	}
	bucket, err := root.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer bucket.Close()
	name := hash + ".bin"
	f, _, err := openVerifiedBlob(bucket, name, hash)
	if err == nil {
		info, statErr := f.Stat()
		f.Close()
		if statErr != nil {
			return statErr
		}
		if runtime.GOOS == "windows" || info.Mode().Perm() == 0600 {
			return nil
		}
		// Replace a legacy file under a new inode, leaving any hard link alone.
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errBlobChecksum) {
		return err
	}
	stage := ".body-" + rand.Text()
	tmp, err := bucket.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer bucket.Remove(stage)
	if err = secureCaptureStage(tmp); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = bucket.Rename(stage, name); err != nil {
		// Windows can refuse to replace an already-published peer's file.
		if other, _, check := openVerifiedBlob(bucket, name, hash); check == nil {
			return other.Close()
		}
		return err
	}
	// Windows does not support fsync on a directory handle. The content was
	// already flushed before the atomic publication.
	if runtime.GOOS == "windows" {
		return nil
	}
	parent, err := bucket.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}
