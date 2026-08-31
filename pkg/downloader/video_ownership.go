package downloader

import (
	"os"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/save_sub_helper"
)

type exactVideoOwnership = save_sub_helper.ExactVideoOwnership

const (
	exactVideoOwnershipNone      = save_sub_helper.ExactVideoOwnershipNone
	exactVideoOwnershipUnique    = save_sub_helper.ExactVideoOwnershipUnique
	exactVideoOwnershipAmbiguous = save_sub_helper.ExactVideoOwnershipAmbiguous
)

// directoryVideoInventory combines known/queued video paths with every actual
// video entry in the directory. Subtitle ownership must be decided against the
// complete sibling inventory before evidence is narrowed back to queued paths.
func directoryVideoInventory(videoDir string, entries []os.DirEntry, knownVideoPaths []string) []string {
	return save_sub_helper.DirectoryVideoInventory(videoDir, entries, knownVideoPaths)
}

// exactVideoOwner resolves a subtitle basename by the longest matching video
// stem. Equal-length matches remain ambiguous (for example the same stem with
// both .mkv and .mp4) and must never become save/queue evidence.
func exactVideoOwner(videoPaths []string, subtitleName string) (string, exactVideoOwnership) {
	return save_sub_helper.ExactVideoOwner(videoPaths, subtitleName)
}
