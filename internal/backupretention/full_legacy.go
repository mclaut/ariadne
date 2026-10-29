package backupretention

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// LegacyFull is an explicit immutable enrollment, never a recursive discovery rule.
type LegacyFull struct {
	Name     string `json:"name"`
	Manifest string `json:"manifest"`
	SHA256   string `json:"sha256"`
}

var fullName = regexp.MustCompile(`^full-[0-9]{8}T[0-9]{6}Z$`)

const legacyRegularFile = "file"

// PruneLegacyFull expires only an explicitly enrolled full set after a newer
// managed full recovery has succeeded. Audit payloads and all root receipts are
// copied and verified in an append-only archive outside the rotation first.
func PruneLegacyFull(root, keeper string, old LegacyFull) error {
	m, err := Verify(keeper)
	if err != nil {
		return err
	}
	if m.Object != "full" || m.Metadata["restore_verified"] != "true" {
		return errors.New("verified full recovery required")
	}
	if !fullName.MatchString(old.Name) || !fs.ValidPath(old.Manifest) || strings.Contains(old.Manifest, "/") || len(old.SHA256) != 64 {
		return errors.New("invalid explicit legacy full enrollment")
	}
	created, err := time.Parse("20060102T150405Z", strings.TrimPrefix(old.Name, "full-"))
	if err != nil || !m.CreatedAt.After(created) {
		return errors.New("legacy full chronology is ambiguous")
	}
	dir := filepath.Join(root, old.Name)
	if _, err = os.Lstat(dir); os.IsNotExist(err) {
		return nil
	}
	physical, err := physicalDirectory(dir)
	if err != nil {
		return err
	}
	dir = physical
	manifest := filepath.Join(dir, old.Manifest)
	digest, err := HashFile(manifest)
	if err != nil {
		return err
	}
	if digest != old.SHA256 {
		return errors.New("legacy full manifest changed")
	}
	data, err := os.ReadFile(manifest) //nolint:gosec // explicitly enrolled regular manifest
	if err != nil {
		return err
	}
	var legacy struct {
		ID      string `json:"id"`
		Entries []struct {
			Path   string `json:"path"`
			Mode   uint32 `json:"mode"`
			Bytes  int64  `json:"bytes"`
			SHA256 string `json:"sha256"`
			Target string `json:"target"`
			Type   string `json:"type"`
		} `json:"entries"`
		Excluded []string `json:"self_excluded"`
	}
	if err = json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if legacy.ID != old.Name {
		return errors.New("legacy full identity mismatch")
	}
	expected := make([]Entry, 0, len(legacy.Entries)+len(legacy.Excluded))
	for _, e := range legacy.Entries {
		if !fs.ValidPath(e.Path) || e.Path == "." {
			return errors.New("invalid legacy full entry")
		}
		entry := Entry{Path: e.Path, Mode: e.Mode, Kind: e.Type, Target: e.Target}
		if e.Type == legacyRegularFile {
			entry.Bytes = e.Bytes
			entry.SHA256 = e.SHA256
		}
		expected = append(expected, entry)
	}
	for _, name := range legacy.Excluded {
		if !fs.ValidPath(name) || strings.Contains(name, "/") {
			return errors.New("invalid legacy receipt name")
		}
		p := filepath.Join(dir, name)
		info, statErr := os.Lstat(p)
		if statErr != nil {
			return statErr
		}
		sha, hashErr := HashFile(p)
		if hashErr != nil {
			return hashErr
		}
		expected = append(expected, Entry{Path: name, Kind: legacyRegularFile, Mode: uint32(info.Mode().Perm()), Bytes: info.Size(), SHA256: sha})
	}
	slices.SortFunc(expected, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	actual, err := inventory(dir)
	if err != nil {
		return err
	}
	if !slices.Equal(actual, expected) {
		return errors.New("legacy full content differs from enrolled manifest")
	}
	busy, err := InUse(dir)
	if err != nil {
		return err
	}
	if busy {
		return errors.New("legacy full in use")
	}
	// Preserve access/activity history, old diagnostic evidence, model/runtime
	// receipts and all top-level metadata. Neither source audit nor existing
	// audit archives are modified or rotated.
	paths := []string{"runtime/state", "runtime/logs", "runtime/metrics.db", "legacy-evidence", "verification"}
	for _, e := range expected {
		if !strings.Contains(e.Path, "/") && e.Kind == legacyRegularFile {
			paths = append(paths, e.Path)
		}
	}
	if _, err = ArchiveFullAudit(filepath.Join(root, "full-audit"), old.SHA256, dir, paths); err != nil {
		return err
	}
	if _, err = Verify(keeper); err != nil {
		return err
	}
	event := map[string]any{"action": "remove_legacy_full", "status": "started", "path": old.Name, "manifest_sha256": old.SHA256}
	if err = appendAudit(root, event); err != nil {
		return err
	}
	// Pin the exact tree by rename into a new empty container. No overwrite.
	retired, err := os.MkdirTemp(root, ".retired-legacy-full-")
	if err != nil {
		return err
	}
	target := filepath.Join(retired, "generation")
	if err = renameExclusive(dir, target); err != nil {
		return err
	}
	actual, err = inventory(target)
	if err != nil {
		return err
	}
	if !slices.Equal(actual, expected) {
		return errors.New("legacy full changed before deletion; retained for diagnosis")
	}
	if err = removeEntries(target, expected, false); err != nil {
		return err
	}
	if err = os.Remove(retired); err != nil {
		return err
	}
	event["status"] = "complete"
	return appendAudit(root, event)
}

func ArchiveFullAudit(root, id, source string, paths []string) (string, error) {
	if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
		return "", errors.New("invalid full audit identity")
	}
	object := "full-" + id
	dest := filepath.Join(root, object)
	if _, err := os.Lstat(dest); err == nil {
		m, verifyErr := Verify(dest)
		if verifyErr != nil {
			return "", verifyErr
		}
		if m.Object != object {
			return "", errors.New("audit archive identity mismatch")
		}
		return dest, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	stage, err := NewStage(root, object)
	if err != nil {
		return "", err
	}
	names := append([]string(nil), paths...)
	slices.Sort(names)
	last := ""
	for _, name := range names {
		if !fs.ValidPath(name) || name == "." {
			return "", errors.New("invalid full audit path")
		}
		if name == last || (last != "" && strings.HasPrefix(name, last+"/")) {
			continue
		}
		last = name
		from := filepath.Join(source, filepath.FromSlash(name))
		if _, err = os.Lstat(from); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return "", err
		}
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(from))
		if parentErr != nil {
			return "", parentErr
		}
		rel, relErr := filepath.Rel(source, parent)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", errors.New("audit parent escapes full set")
		}
		// copyItem preserves root links rather than following them outside the full set.
		if err = copyItem(from, filepath.Join(stage, "evidence", filepath.FromSlash(name))); err != nil {
			return "", err
		}
	}
	return Publish(root, object, stage, time.Now(), map[string]string{"source_manifest_sha256": id, "purpose": "preserved-full-backup-audit"})
}
