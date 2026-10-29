package main

import (
	"ariadne/internal/backupretention"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Append-only logs have a bounded capture window: later appends do not prevent
// backup, but rewriting/truncating/replacing the captured prefix does.
func copyFullPrefix(source, dest string) error {
	before, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return errors.New("prefix source must be regular")
	}
	f, err := os.Open(source) //nolint:gosec // explicit operator-owned append-only source
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, opened) {
		return errors.New("prefix source replaced")
	}
	if err = os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // exclusive private full stage
	if err != nil {
		return err
	}
	n, copyErr := io.CopyN(out, f, opened.Size())
	syncErr := out.Sync()
	closeErr := out.Close()
	if err = errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	if n != opened.Size() {
		return errors.New("prefix source shortened")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	if _, err = io.CopyN(h, f, opened.Size()); err != nil {
		return err
	}
	current, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !os.SameFile(opened, current) || current.Size() < opened.Size() {
		return errors.New("prefix source changed")
	}
	sha, err := backupretention.HashFile(dest)
	if err != nil {
		return err
	}
	if sha != hex.EncodeToString(h.Sum(nil)) {
		return errors.New("prefix content changed during capture")
	}
	return nil
}
