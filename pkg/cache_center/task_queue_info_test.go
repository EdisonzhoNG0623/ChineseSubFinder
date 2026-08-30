package cache_center

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestUnsupportedDirectorySyncError(t *testing.T) {
	for _, err := range []error{
		syscall.EINVAL,
		syscall.ENOTSUP,
		&os.PathError{Op: "sync", Path: "/cache", Err: syscall.EINVAL},
		&os.PathError{Op: "sync", Path: "/cache", Err: syscall.ENOTSUP},
	} {
		if !isUnsupportedDirectorySyncError(err) {
			t.Errorf("isUnsupportedDirectorySyncError(%v) = false", err)
		}
	}
	if isUnsupportedDirectorySyncError(errors.New("I/O error")) {
		t.Fatal("unrelated directory sync error classified as unsupported")
	}
}
