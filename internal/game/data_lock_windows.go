//go:build windows

package game

import (
	"os"
	"path/filepath"
)

func lockData(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "server.lock.exclusive")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	return func() { f.Close(); os.Remove(path) }, nil
}
