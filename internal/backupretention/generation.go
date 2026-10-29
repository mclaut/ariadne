package backupretention

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const manifestName = "manifest.json"

type Entry struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Bytes  int64  `json:"bytes,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Target string `json:"target,omitempty"`
	Kind   string `json:"kind"`
}

type Manifest struct {
	Format    int               `json:"format"`
	Object    string            `json:"object"`
	CreatedAt time.Time         `json:"created_at"`
	Verified  bool              `json:"verified"`
	Entries   []Entry           `json:"entries"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func validObject(object string) bool {
	return object != "" && object != "." && object != ".." && !strings.ContainsAny(object, "/\\\x00")
}

func NewStage(root, object string) (string, error) {
	if !validObject(object) || !filepath.IsAbs(root) {
		return "", errors.New("invalid backup object")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	physical, err := physicalDirectory(root)
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(physical, ".pending-"+object+"-")
}

func physicalDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("backup path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("backup directory must not be a symlink")
	}
	return filepath.EvalSymlinks(path)
}

func HashFile(path string) (string, error) {
	initial, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !initial.Mode().IsRegular() {
		return "", errors.New("backup hash input must be a regular file")
	}
	f, err := os.Open(path) //nolint:gosec // caller supplies a validated backup path
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", errors.New("not a regular backup file")
	}
	if !os.SameFile(initial, before) {
		return "", errors.New("backup hash input changed before reading")
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	after, err := f.Stat()
	if err != nil {
		return "", err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !os.SameFile(before, current) || !os.SameFile(before, after) || before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) {
		return "", errors.New("backup file changed while hashing")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func inventory(dir string) ([]Entry, error) {
	var entries []Entry
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == manifestName {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := Entry{Path: rel, Mode: uint32(info.Mode().Perm())}
		switch {
		case d.Type()&os.ModeSymlink != 0:
			e.Kind = "symlink"
			e.Target, err = os.Readlink(path)
		case d.IsDir():
			e.Kind = "directory"
		case info.Mode().IsRegular():
			e.Kind = "file"
			e.Bytes = info.Size()
			e.SHA256, err = HashFile(path)
		default:
			return fmt.Errorf("unsupported backup entry %q", rel)
		}
		if err != nil {
			return err
		}
		entries = append(entries, e)
		return nil
	})
	return entries, err
}

func Verify(dir string) (Manifest, error) {
	var m Manifest
	physical, err := physicalDirectory(dir)
	if err != nil {
		return m, err
	}
	dir = physical
	manifestInfo, err := os.Lstat(filepath.Join(dir, manifestName))
	if err != nil {
		return m, err
	}
	if !manifestInfo.Mode().IsRegular() {
		return m, errors.New("backup manifest must be a regular file")
	}
	data, err := os.ReadFile(filepath.Join(dir, manifestName)) //nolint:gosec // managed backup manifest
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Format != 1 || !m.Verified || !validObject(m.Object) || m.CreatedAt.IsZero() {
		return m, errors.New("unverified backup manifest")
	}
	entries, err := inventory(dir)
	if err != nil {
		return m, err
	}
	if !slices.Equal(entries, m.Entries) {
		return m, errors.New("backup contents differ from verified manifest")
	}
	return m, nil
}

// Publish commits an independently verified stage and keeps exactly one generation.
// Unknown or modified prior generations block replacement; they are never erased.
func Publish(root, object, stage string, created time.Time, metadata map[string]string) (string, error) {
	physical, err := physicalDirectory(root)
	if err != nil {
		return "", err
	}
	root = physical
	if !validObject(object) || filepath.Dir(stage) != filepath.Clean(root) ||
		!strings.HasPrefix(filepath.Base(stage), ".pending-"+object+"-") {
		return "", errors.New("invalid staged backup path")
	}
	physicalStage, err := physicalDirectory(stage)
	if err != nil || physicalStage != stage {
		return "", errors.New("stage must be a physical directory")
	}
	unlock, err := Lock(root, object)
	if err != nil {
		return "", err
	}
	defer unlock()
	entries, err := inventory(stage)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", errors.New("empty backup stage")
	}
	m := Manifest{Format: 1, Object: object, CreatedAt: created.UTC(), Verified: true, Entries: entries, Metadata: metadata}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(filepath.Join(stage, manifestName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // new managed stage
	if err != nil {
		return "", err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return "", err
	}
	if _, err = Verify(stage); err != nil {
		return "", err
	}
	if err = syncDirectory(stage); err != nil {
		return "", err
	}
	destination := filepath.Join(root, object)
	var retired string
	if _, statErr := os.Lstat(destination); statErr == nil {
		old, verifyErr := Verify(destination)
		if verifyErr != nil {
			return "", fmt.Errorf("previous backup: %w", verifyErr)
		}
		busy, useErr := InUse(destination)
		if useErr != nil {
			return "", useErr
		}
		if busy {
			return "", errors.New("previous backup is in use; replacement deferred")
		}
		oldSHA, hashErr := HashFile(filepath.Join(destination, manifestName))
		if hashErr != nil {
			return "", hashErr
		}
		newSHA, hashErr := HashFile(filepath.Join(stage, manifestName))
		if hashErr != nil {
			return "", hashErr
		}
		selection := OnePrevious([]Copy{
			{Path: destination, Object: old.Object, SHA256: oldSHA, CreatedAt: old.CreatedAt, Verified: true, InUse: busy},
			{Path: stage, Object: object, SHA256: newSHA, CreatedAt: created, Verified: true},
		})
		if old.Object != object || !created.After(old.CreatedAt) || len(selection.Remove) != 1 || selection.Remove[0].Path != destination {
			return "", errors.New("backup is not newer than the retained generation")
		}
		if err = appendAudit(root, map[string]any{
			"action": "replace_previous", "status": "started", "object": object,
			"old_manifest_sha256": oldSHA, "new_manifest_sha256": newSHA,
		}); err != nil {
			return "", err
		}
		retired, err = os.MkdirTemp(root, ".retired-"+object+"-")
		if err != nil {
			return "", err
		}
		// Rename into a newly created empty container, never over an existing generation.
		retired = filepath.Join(retired, "generation")
		if err = renameExclusive(destination, retired); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	if err = renameExclusive(stage, destination); err != nil {
		if retired != "" {
			err = errors.Join(err, renameExclusive(retired, destination))
		}
		return "", err
	}
	if _, err = Verify(destination); err != nil {
		return destination, err
	}
	if err = syncDirectory(root); err != nil {
		return destination, err
	}
	if retired != "" {
		if err = RemoveVerified(retired); err != nil {
			return destination, fmt.Errorf("new backup retained; old generation cleanup: %w", err)
		}
		if err = os.Remove(filepath.Dir(retired)); err != nil {
			return destination, err
		}
	}
	if err = appendAudit(root, map[string]any{
		"action": "replace_previous", "status": "complete", "object": object,
	}); err != nil {
		return destination, err
	}
	return destination, nil
}

// RemoveVerified removes only the exact immutable entries of a managed generation.
func RemoveVerified(dir string) error {
	m, err := Verify(dir)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	entries := append([]Entry(nil), m.Entries...)
	slices.SortFunc(entries, func(a, b Entry) int {
		da, db := strings.Count(a.Path, "/"), strings.Count(b.Path, "/")
		if da != db {
			return db - da
		}
		return strings.Compare(b.Path, a.Path)
	})
	for _, e := range entries {
		if !fs.ValidPath(e.Path) || e.Path == "." || e.Path == manifestName {
			return errors.New("invalid managed manifest entry")
		}
		info, statErr := root.Lstat(filepath.FromSlash(e.Path))
		if statErr != nil {
			return statErr
		}
		switch e.Kind {
		case "file":
			if !info.Mode().IsRegular() || info.Size() != e.Bytes {
				return errors.New("backup entry changed")
			}
			sha, hashErr := HashFile(filepath.Join(dir, filepath.FromSlash(e.Path)))
			if hashErr != nil {
				return hashErr
			}
			if sha != e.SHA256 {
				return errors.New("backup entry checksum changed")
			}
		case "symlink":
			target, linkErr := root.Readlink(filepath.FromSlash(e.Path))
			if linkErr != nil {
				return linkErr
			}
			if target != e.Target {
				return errors.New("backup link changed")
			}
		case "directory":
			if !info.IsDir() {
				return errors.New("backup directory changed")
			}
		default:
			return errors.New("unknown backup entry type")
		}
		if err = root.Remove(filepath.FromSlash(e.Path)); err != nil {
			return err
		}
	}
	if err = root.Remove(manifestName); err != nil {
		return err
	}
	return os.Remove(dir)
}
