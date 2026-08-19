package log_helper

import (
	"os"
	"sort"
)

type onceLogFile struct {
	path    string
	modTime int64
}

// trimOnceLogs keeps the newest maxCount readable once logs. File names contain
// job IDs and are not chronological, so retention must use modification time.
func trimOnceLogs(matches []string, maxCount int) {
	if maxCount < 0 {
		maxCount = 0
	}

	files := make([]onceLogFile, 0, len(matches))
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}
		files = append(files, onceLogFile{path: match, modTime: info.ModTime().UnixNano()})
	}
	if len(files) <= maxCount {
		return
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime == files[j].modTime {
			return files[i].path < files[j].path
		}
		return files[i].modTime < files[j].modTime
	})
	for _, file := range files[:len(files)-maxCount] {
		_ = os.Remove(file.path)
	}
}
