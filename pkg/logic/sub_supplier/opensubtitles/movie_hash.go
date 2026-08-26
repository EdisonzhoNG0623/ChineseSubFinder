package opensubtitles

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const movieHashBlockSize int64 = 64 * 1024

// movieHash implements the OpenSubtitles 64-bit movie hash. Small and virtual
// media files are deliberately skipped; callers can still search by IMDb/TMDB.
func movieHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() < 2*movieHashBlockSize {
		return "", errors.New("file is too small for OpenSubtitles hash")
	}

	hash := uint64(info.Size())
	for _, offset := range []int64{0, info.Size() - movieHashBlockSize} {
		if _, err = file.Seek(offset, io.SeekStart); err != nil {
			return "", err
		}
		buffer := make([]byte, movieHashBlockSize)
		if _, err = io.ReadFull(file, buffer); err != nil {
			return "", err
		}
		for index := 0; index < len(buffer); index += 8 {
			hash += binary.LittleEndian.Uint64(buffer[index : index+8])
		}
	}
	return fmt.Sprintf("%016x", hash), nil
}
