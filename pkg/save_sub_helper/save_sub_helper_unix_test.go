//go:build !windows

package save_sub_helper

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriteSubtitleFileAtomicallyHonorsUmaskForNewTarget(t *testing.T) {
	root := t.TempDir()
	previousUmask := syscall.Umask(0o077)
	defer syscall.Umask(previousUmask)

	targetPath := filepath.Join(root, "Episode.zh.srt")
	if err := writeSubtitleFileAtomically(targetPath, []byte("subtitle")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("subtitle mode = %04o, want umask-restricted mode 0600", got)
	}
}
