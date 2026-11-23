package traffic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestPrivateAtomicCaptureBodies(t *testing.T) {
	tr := &Traffic{dir: t.TempDir()}
	body := bytes.Repeat([]byte("synthetic fixture"), 16000)
	digest := sha256.Sum256(body)
	hash := hex.EncodeToString(digest[:])
	path := filepath.Join(tr.dir, "_blobs", "sha256", hash[:2], hash+".bin")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body[:9], 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tr.Blob(hash); !errors.Is(err, errBlobChecksum) {
		t.Fatalf("partial body: %v", err)
	}
	var writers sync.WaitGroup
	for range 4 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			if err := tr.publishBlob(hash, body); err != nil {
				t.Error(err)
			}
		}()
	}
	writers.Wait()
	f, size, err := tr.Blob(hash)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(body, got) || size != int64(len(body)) {
		t.Fatalf("invalid published body (%d bytes, %v)", size, err)
	}
	if runtime.GOOS != "windows" {
		for file, mode := range map[string]os.FileMode{path: 0600, filepath.Dir(path): 0700} {
			st, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != mode {
				t.Errorf("%s mode %v", file, st.Mode())
			}
		}
	}
	if entries, err := os.ReadDir(filepath.Dir(path)); err != nil || len(entries) != 1 {
		t.Fatalf("unexpected staged files: %v, %v", entries, err)
	}
}
