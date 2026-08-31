package save_sub_helper

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_parser_hub"
	timelineFixerArtifacts "github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_timeline_fixer"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/subparser"
)

// VideoSubtitleMarkerSnapshot is an inode-aware view of a marked subtitle.
// The paths are exposed for diagnostics and focused tests; file identities stay
// private so only this package can perform the safe demotion operation.
type VideoSubtitleMarkerSnapshot struct {
	videoPath        string
	markedPath       string
	unmarkedPath     string
	fileInfo         os.FileInfo
	unmarkedFileInfo os.FileInfo
	backupFileInfo   os.FileInfo
	markedBackup     string
	unmarkedBackup   string
}

func (s VideoSubtitleMarkerSnapshot) VideoPath() string    { return s.videoPath }
func (s VideoSubtitleMarkerSnapshot) MarkedPath() string   { return s.markedPath }
func (s VideoSubtitleMarkerSnapshot) UnmarkedPath() string { return s.unmarkedPath }

// SnapshotSubtitleMarkers records only markers uniquely owned by this
// transaction's exact video path. Call it before publishing replacement
// subtitles, then call DemoteSubtitleMarkers only after all writes succeed.
func (w *VideoWriteTransaction) SnapshotSubtitleMarkers() ([]VideoSubtitleMarkerSnapshot, error) {
	videoPath := w.videoFileFullPath
	videoDir := filepath.Dir(videoPath)
	videoBase := filepath.Base(videoPath)
	videoStem := strings.TrimSuffix(videoBase, filepath.Ext(videoBase))
	entries, err := os.ReadDir(videoDir)
	if err != nil {
		return nil, err
	}
	videoInventory := DirectoryVideoInventory(videoDir, entries, []string{videoPath})
	cleanVideoPath := filepath.Clean(videoPath)
	snapshots := make([]VideoSubtitleMarkerSnapshot, 0)
	for _, entry := range entries {
		if entry.IsDir() || !sub_parser_hub.IsSubExtWanted(entry.Name()) {
			continue
		}
		ownerPath, ownership := ExactVideoOwner(videoInventory, entry.Name())
		if ownership != ExactVideoOwnershipUnique || ownerPath != cleanVideoPath {
			continue
		}
		// Start at the delimiter immediately after the exact video stem so an
		// immediate "Movie.default.srt" marker remains visible, while a token in
		// the video stem itself can never be mistaken for a subtitle marker.
		unmarkedName, marked := subtitleNameWithoutDefaultForcedMarker(entry.Name(), len(videoStem))
		if !marked {
			continue
		}
		markedPath := filepath.Join(videoDir, entry.Name())
		fileInfo, statErr := os.Stat(markedPath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return nil, statErr
		}
		if !fileInfo.Mode().IsRegular() {
			continue
		}
		snapshot := VideoSubtitleMarkerSnapshot{
			videoPath:    canonicalVideoWritePath(videoPath),
			markedPath:   markedPath,
			unmarkedPath: filepath.Join(videoDir, unmarkedName),
			fileInfo:     fileInfo,
		}
		if unmarkedInfo, unmarkedErr := os.Stat(snapshot.unmarkedPath); unmarkedErr == nil && unmarkedInfo.Mode().IsRegular() {
			snapshot.unmarkedFileInfo = unmarkedInfo
		}
		snapshot.markedBackup = markedPath + timelineFixerArtifacts.BackUpExt
		snapshot.unmarkedBackup = snapshot.unmarkedPath + timelineFixerArtifacts.BackUpExt
		if backupInfo, backupErr := os.Stat(snapshot.markedBackup); backupErr == nil && backupInfo.Mode().IsRegular() {
			snapshot.backupFileInfo = backupInfo
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func subtitleNameWithoutDefaultForcedMarker(name string, suffixStart int) (string, bool) {
	if suffixStart < 0 || suffixStart > len(name) {
		return name, false
	}
	markerTokens := []string{
		strings.TrimPrefix(subparser.Sub_Ext_Mark_Default, "."),
		strings.TrimPrefix(subparser.Sub_Ext_Mark_Forced, "."),
	}
	// Walk dot-delimited tokens in the original string. This preserves byte
	// indices for Unicode names and recognizes case variants such as DEFAULT.
	for markerStart := suffixStart; markerStart < len(name); markerStart++ {
		if name[markerStart] != '.' {
			continue
		}
		relativeEnd := strings.IndexByte(name[markerStart+1:], '.')
		if relativeEnd < 0 {
			break
		}
		markerEnd := markerStart + 1 + relativeEnd
		token := name[markerStart+1 : markerEnd]
		for _, markerToken := range markerTokens {
			if strings.EqualFold(token, markerToken) {
				return name[:markerStart] + "." + name[markerEnd+1:], true
			}
		}
		markerStart = markerEnd - 1
	}
	return name, false
}

// DemoteSubtitleMarkers removes default/forced markers captured before this
// transaction. Inode checks prevent a newly published same-path subtitle from
// being demoted and prevent a stale snapshot from overwriting a new unmarked
// subtitle.
func (w *VideoWriteTransaction) DemoteSubtitleMarkers(snapshots []VideoSubtitleMarkerSnapshot) {
	for _, snapshot := range snapshots {
		if snapshot.videoPath != canonicalVideoWritePath(w.videoFileFullPath) {
			w.helper.log.WithFields(map[string]interface{}{
				"snapshot_video_path": snapshot.videoPath,
				"transaction_video":   canonicalVideoWritePath(w.videoFileFullPath),
			}).Warnln("refuse subtitle marker snapshot from another video transaction")
			continue
		}
		currentInfo, err := os.Stat(snapshot.markedPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			w.helper.log.WithError(err).Warnln("inspect existing subtitle marker after save", snapshot.markedPath)
			continue
		}
		if !os.SameFile(snapshot.fileInfo, currentInfo) {
			continue
		}
		currentUnmarkedInfo, unmarkedErr := os.Stat(snapshot.unmarkedPath)
		unmarkedPublished := false
		if unmarkedErr == nil {
			unmarkedPublished = snapshot.unmarkedFileInfo == nil || !os.SameFile(snapshot.unmarkedFileInfo, currentUnmarkedInfo)
		} else if !os.IsNotExist(unmarkedErr) {
			w.helper.log.WithError(unmarkedErr).Warnln("inspect unmarked subtitle after save", snapshot.unmarkedPath)
			continue
		}
		if unmarkedPublished {
			if err = os.Remove(snapshot.markedPath); err != nil {
				w.helper.log.WithError(err).Warnln("remove superseded marked subtitle", snapshot.markedPath)
				continue
			}
			w.removeUnchangedMarkedBackup(snapshot)
			continue
		}
		if err = os.Rename(snapshot.markedPath, snapshot.unmarkedPath); err != nil {
			w.helper.log.WithError(err).Warnln("demote superseded subtitle marker", snapshot.markedPath)
			continue
		}
		if snapshot.backupFileInfo == nil {
			w.removeRegularSubtitleBackup(snapshot.unmarkedBackup, "remove stale unmarked subtitle backup")
			continue
		}
		currentBackupInfo, backupErr := os.Stat(snapshot.markedBackup)
		if os.IsNotExist(backupErr) {
			w.removeRegularSubtitleBackup(snapshot.unmarkedBackup, "remove stale unmarked subtitle backup")
			continue
		}
		if backupErr != nil {
			w.helper.log.WithError(backupErr).Warnln("inspect superseded marked subtitle backup", snapshot.markedBackup)
			w.removeRegularSubtitleBackup(snapshot.unmarkedBackup, "remove stale unmarked subtitle backup")
			continue
		}
		if !os.SameFile(snapshot.backupFileInfo, currentBackupInfo) {
			w.removeRegularSubtitleBackup(snapshot.unmarkedBackup, "remove stale unmarked subtitle backup")
			continue
		}
		if backupErr = os.Rename(snapshot.markedBackup, snapshot.unmarkedBackup); backupErr != nil {
			w.helper.log.WithError(backupErr).Warnln("demote superseded marked subtitle backup", snapshot.markedBackup)
			w.removeRegularSubtitleBackup(snapshot.markedBackup, "remove stale marked subtitle backup")
			w.removeRegularSubtitleBackup(snapshot.unmarkedBackup, "remove stale unmarked subtitle backup")
		}
	}
}

func (w *VideoWriteTransaction) removeRegularSubtitleBackup(path, message string) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		w.helper.log.WithError(err).Warnln(message, path)
		return
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		w.helper.log.WithField("path", path).Warnln(message, "refused non-file backup")
		return
	}
	if err = os.Remove(path); err != nil {
		w.helper.log.WithError(err).Warnln(message, path)
	}
}

func (w *VideoWriteTransaction) removeUnchangedMarkedBackup(snapshot VideoSubtitleMarkerSnapshot) {
	if snapshot.backupFileInfo == nil {
		return
	}
	currentBackupInfo, err := os.Stat(snapshot.markedBackup)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		w.helper.log.WithError(err).Warnln("inspect superseded marked subtitle backup", snapshot.markedBackup)
		return
	}
	if !os.SameFile(snapshot.backupFileInfo, currentBackupInfo) {
		return
	}
	if err = os.Remove(snapshot.markedBackup); err != nil {
		w.helper.log.WithError(err).Warnln("remove superseded marked subtitle backup", snapshot.markedBackup)
	}
}
