package backupretention

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// RemoveFile removes one exact obsolete backup after an exclusive staging rename.
func RemoveFile(path, expectedSHA string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("obsolete backup must be a regular file")
	}
	busy, err := InUse(path)
	if err != nil {
		return err
	}
	if busy {
		return errors.New("obsolete backup is in use; cleanup deferred")
	}
	// Windows FileInfo may load IDs lazily from the original pathname.
	pin, err := os.Open(path) //nolint:gosec // exact obsolete regular backup checked above
	if err != nil {
		return err
	}
	opened, statErr := pin.Stat()
	same := statErr == nil && os.SameFile(info, opened)
	closeErr := pin.Close()
	if statErr != nil || closeErr != nil {
		return errors.Join(statErr, closeErr)
	}
	if !same {
		return errors.New("obsolete backup replaced before staging")
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	staged := filepath.Join(filepath.Dir(path), ".retention-"+hex.EncodeToString(random[:]))
	if err = renameExclusive(path, staged); err != nil {
		return err
	}
	restore := func(cause error) error { return errors.Join(cause, renameExclusive(staged, path)) }
	actual, err := os.Lstat(staged)
	if err != nil {
		return restore(err)
	}
	if !os.SameFile(info, actual) || info.Size() != actual.Size() || !info.ModTime().Equal(actual.ModTime()) {
		return restore(errors.New("obsolete backup changed before removal"))
	}
	sha, err := HashFile(staged)
	if err != nil {
		return restore(err)
	}
	if sha != expectedSHA {
		return restore(errors.New("obsolete backup checksum changed"))
	}
	if err = os.Remove(staged); err != nil {
		return restore(err)
	}
	return syncDirectory(filepath.Dir(path))
}
