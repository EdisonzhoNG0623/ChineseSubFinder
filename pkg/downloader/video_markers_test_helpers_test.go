package downloader

import (
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/save_sub_helper"
	"github.com/sirupsen/logrus"
)

type videoSubtitleMarkerSnapshot = save_sub_helper.VideoSubtitleMarkerSnapshot

func snapshotVideoSubtitleMarkers(videoPath string) ([]videoSubtitleMarkerSnapshot, error) {
	helper := save_sub_helper.NewSaveSubHelper(logrus.New(), nil, nil)
	var snapshots []videoSubtitleMarkerSnapshot
	err := helper.WithVideoWriteLock(videoPath, func(writer *save_sub_helper.VideoWriteTransaction) error {
		var snapshotErr error
		snapshots, snapshotErr = writer.SnapshotSubtitleMarkers()
		return snapshotErr
	})
	return snapshots, err
}

func demoteVideoSubtitleMarkers(log *logrus.Logger, snapshots []videoSubtitleMarkerSnapshot) {
	if len(snapshots) == 0 {
		return
	}
	helper := save_sub_helper.NewSaveSubHelper(log, nil, nil)
	_ = helper.WithVideoWriteLock(snapshots[0].VideoPath(), func(writer *save_sub_helper.VideoWriteTransaction) error {
		writer.DemoteSubtitleMarkers(snapshots)
		return nil
	})
}
