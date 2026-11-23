//go:build !linux

package dostoevsky

import "os"

func syncJournalData(file *os.File) error { return file.Sync() }
