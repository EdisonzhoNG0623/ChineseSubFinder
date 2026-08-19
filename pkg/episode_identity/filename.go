package episode_identity

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var standaloneEpisodeNumber = regexp.MustCompile(`(?i)(?:^|[^\pL\pN])(?:ep?|#)?0*([1-9][0-9]{0,3})(?:[^\pL\pN]|$)`)

// FilenameContainsAbsoluteEpisode matches a standalone absolute episode token
// while excluding common years. It intentionally does not treat digits inside
// codecs/resolutions (x264, 1080p) as episode numbers.
func FilenameContainsAbsoluteEpisode(name string, episode int) bool {
	if episode <= 0 {
		return false
	}
	name = strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	for _, match := range standaloneEpisodeNumber.FindAllStringSubmatch(name, -1) {
		value, err := strconv.Atoi(match[1])
		if err != nil || value >= 1900 && value <= 2099 {
			continue
		}
		if value == episode {
			return true
		}
	}
	return false
}
