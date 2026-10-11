package benchmark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// DefaultDataDir is the download cache: $XDG_CACHE_HOME/memd-eval, else ~/.cache/memd-eval.
func DefaultDataDir() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "memd-eval")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cache", "memd-eval")
	}
	return filepath.Join(os.TempDir(), "memd-eval")
}

// Fetch returns the path of b's dataset under dataDir/<benchmark>/, downloading it first when it is
// missing or its sha256 does not match. The download goes to a temporary file that is renamed into
// place only once its sha256 matches. A benchmark without a Dataset returns "".
func Fetch(ctx context.Context, client *http.Client, dataDir string, b Benchmark) (string, error) {
	f := b.Dataset()
	if f.URL == "" {
		return "", nil
	}
	dir := filepath.Join(dataDir, b.Name())
	path := filepath.Join(dir, f.Name)
	if sum, err := HashPath(path); err == nil && sum == f.SHA256 {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("benchmark: create %s: %w", dir, err)
	}
	if err := download(ctx, client, f, path); err != nil {
		return "", fmt.Errorf("benchmark: download %s: %w", f.URL, err)
	}
	return path, nil
}

// download streams f.URL into a temporary file next to path, checks its sha256 and renames it to path.
func download(ctx context.Context, client *http.Client, f File, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), f.Name+".part-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != f.SHA256 {
		return fmt.Errorf("sha256 %s, want %s", sum, f.SHA256)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

// HashPath returns the hex sha256 of a file, or of a directory's regular files (each file's relative
// path and contents, in path order).
func HashPath(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("benchmark: hash %s: %w", path, err)
	}
	h := sha256.New()
	if !info.IsDir() {
		if err := hashFile(h, path); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	var files []string
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			files = append(files, p)
		}
		return err
	})
	if err != nil {
		return "", fmt.Errorf("benchmark: hash %s: %w", path, err)
	}
	sort.Strings(files)
	for _, p := range files {
		rel, _ := filepath.Rel(path, p)
		_, _ = h.Write([]byte(rel + "\x00"))
		if err := hashFile(h, p); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(w io.Writer, path string) error {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("benchmark: hash %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("benchmark: hash %s: %w", path, err)
	}
	return nil
}
