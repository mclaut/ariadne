package backupretention

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func InUse(dir string) (bool, error) {
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, process := range processes {
		if _, err = strconv.Atoi(process.Name()); err != nil {
			continue
		}
		proc := filepath.Join("/proc", process.Name())
		info, statErr := os.Stat(proc)
		if statErr != nil {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != uint32(os.Getuid()) {
			continue
		} //nolint:gosec // OS user ID fits uid_t
		for _, name := range []string{"exe", "cwd"} {
			target, linkErr := os.Readlink(filepath.Join(proc, name))
			if linkErr == nil {
				target = strings.TrimSuffix(target, " (deleted)")
				if target == dir || strings.HasPrefix(target, dir+string(os.PathSeparator)) {
					return true, nil
				}
			}
		}
		handles, readErr := os.ReadDir(filepath.Join(proc, "fd"))
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return false, readErr
		}
		for _, fd := range handles {
			target, linkErr := os.Readlink(filepath.Join(proc, "fd", fd.Name()))
			if os.IsNotExist(linkErr) {
				continue
			}
			if linkErr != nil {
				return false, linkErr
			}
			target = strings.TrimSuffix(target, " (deleted)")
			if target == dir || strings.HasPrefix(target, dir+string(os.PathSeparator)) {
				return true, nil
			}
		}
	}
	return false, nil
}
