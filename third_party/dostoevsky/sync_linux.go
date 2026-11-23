//go:build linux

package dostoevsky

import (
	"os"
	"syscall"
)

func syncJournalData(file *os.File) error {
	// Keep the descriptor valid across the syscall, including interrupted calls.
	raw, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var syncErr error
	err = raw.Control(func(fd uintptr) {
		for {
			syncErr = syscall.Fdatasync(int(fd))
			if syncErr != syscall.EINTR {
				break
			}
		}
	})
	if err != nil {
		return err
	}
	if syncErr == syscall.ENOSYS || syncErr == syscall.EOPNOTSUPP {
		return file.Sync()
	}
	return syncErr
}
