package save_sub_helper

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
)

type ExactVideoOwnership int

const (
	ExactVideoOwnershipNone ExactVideoOwnership = iota
	ExactVideoOwnershipUnique
	ExactVideoOwnershipAmbiguous
)

// DirectoryVideoInventory combines known video paths with every actual video
// entry in the directory. Subtitle ownership must be resolved against the
// complete sibling inventory before it is narrowed to one target video.
func DirectoryVideoInventory(videoDir string, entries []os.DirEntry, knownVideoPaths []string) []string {
	videoDir = filepath.Clean(videoDir)
	wantedExtensions := map[string]struct{}{
		common.VideoExtMp4:  {},
		common.VideoExtMkv:  {},
		common.VideoExtRmvb: {},
		common.VideoExtIso:  {},
		common.VideoExtM2ts: {},
	}
	if currentSettings, initialized := settings.GetIfInitialized(); initialized &&
		currentSettings.AdvancedSettings != nil {
		for _, extension := range currentSettings.AdvancedSettings.CustomVideoExts {
			extension = strings.ToLower(strings.TrimSpace(extension))
			if extension == "" {
				continue
			}
			if !strings.HasPrefix(extension, ".") {
				extension = "." + extension
			}
			wantedExtensions[extension] = struct{}{}
		}
	}

	videoPaths := make([]string, 0, len(knownVideoPaths)+len(entries))
	seenPaths := make(map[string]struct{}, cap(videoPaths))
	addPath := func(videoPath string) {
		if videoPath == "" || filepath.Clean(filepath.Dir(videoPath)) != videoDir {
			return
		}
		cleanPath := filepath.Clean(videoPath)
		if _, seen := seenPaths[cleanPath]; seen {
			return
		}
		seenPaths[cleanPath] = struct{}{}
		videoPaths = append(videoPaths, cleanPath)
	}
	for _, videoPath := range knownVideoPaths {
		if extension := strings.ToLower(filepath.Ext(videoPath)); extension != "" {
			// A current/queued path is explicit evidence that its extension is a
			// video extension even in a focused test or with a custom media type.
			wantedExtensions[extension] = struct{}{}
		}
		addPath(videoPath)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, wanted := wantedExtensions[strings.ToLower(filepath.Ext(entry.Name()))]; !wanted {
			continue
		}
		addPath(filepath.Join(videoDir, entry.Name()))
	}
	return videoPaths
}

// ExactVideoOwner resolves a subtitle basename by the longest matching video
// stem. Equal-length matches (for example one stem with both .mkv and .mp4)
// remain ambiguous and are never assigned to either video.
func ExactVideoOwner(videoPaths []string, subtitleName string) (string, ExactVideoOwnership) {
	lowerSubtitleName := strings.ToLower(subtitleName)
	bestPath := ""
	bestScore := -1
	ambiguous := false
	for _, videoPath := range videoPaths {
		videoBase := strings.ToLower(filepath.Base(videoPath))
		videoStem := strings.TrimSuffix(videoBase, filepath.Ext(videoBase))
		if !strings.HasPrefix(lowerSubtitleName, videoStem+".") {
			continue
		}
		score := len(videoStem)
		switch {
		case score > bestScore:
			bestPath, bestScore, ambiguous = filepath.Clean(videoPath), score, false
		case score == bestScore && filepath.Clean(videoPath) != bestPath:
			ambiguous = true
		}
	}
	if bestScore < 0 {
		return "", ExactVideoOwnershipNone
	}
	if ambiguous {
		return "", ExactVideoOwnershipAmbiguous
	}
	return bestPath, ExactVideoOwnershipUnique
}
