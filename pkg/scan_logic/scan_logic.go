package scan_logic

import (
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/dao"
	"github.com/ChineseSubFinder/ChineseSubFinder/internal/models"
)

type ScanLogic struct {
	l                   *logrus.Logger
	scanLogicMap        sync.Map
	stateMu             sync.Mutex
	loadSkipInfosByUID  func(uid string) ([]models.SkipScanInfo, error)
	persistSkipScanInfo func(skipInfo *models.SkipScanInfo) error
}

func NewScanLogic(l *logrus.Logger) *ScanLogic {

	s := &ScanLogic{
		l: l,
	}
	// 那么尝试读取数据库，进行缓存，仅执行一次
	var skipInfos []*models.SkipScanInfo
	result := dao.GetDb().Find(&skipInfos)
	if result.Error != nil && l != nil {
		l.WithError(result.Error).Errorln("load scan skip decisions")
	}
	for _, skipInfo := range skipInfos {
		s.scanLogicMap.Store(skipInfo.UID, skipInfo.Skip)
	}

	return s
}

// Set 设置跳过扫描的信息
func (s *ScanLogic) Set(skipInfo *models.SkipScanInfo) {
	if err := s.set(skipInfo); err != nil && s.l != nil {
		s.l.WithError(err).Errorln("persist scan skip decision", skipInfo.UID)
	}
}

func (s *ScanLogic) set(skipInfo *models.SkipScanInfo) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.scanLogicMap.Store(skipInfo.UID, skipInfo.Skip)
	return s.persist(skipInfo)
}

// SetExactVideoPathSkip persists a media-type-independent decision for one
// exact video. Manual uploads use this because subtitle naming format does not
// reveal whether the target is a movie or an episode.
func (s *ScanLogic) SetExactVideoPathSkip(videoPath string, skip bool) error {
	return s.set(models.NewSkipScanInfoByVideoPath(videoPath, skip))
}

// SetVideoPathSkip updates both the exact-path decision and the legacy
// media-type-specific decision. Keeping both makes old records compatible and
// lets an explicit UI unskip clear a prior manual-upload override.
func (s *ScanLogic) SetVideoPathSkip(videoType int, videoPath string, skip bool) error {
	exactErr := s.SetExactVideoPathSkip(videoPath, skip)
	var legacyInfo *models.SkipScanInfo
	if videoType == 0 {
		legacyInfo = models.NewSkipScanInfoByMovie(videoPath, skip)
	} else {
		legacyInfo = models.NewSkipScanInfoBySeriesEx(videoPath, skip)
	}
	legacyErr := s.set(legacyInfo)
	if exactErr != nil && legacyErr != nil {
		return fmt.Errorf("persist exact-path skip: %v; persist legacy skip: %w", exactErr, legacyErr)
	}
	if exactErr != nil {
		return fmt.Errorf("persist exact-path skip: %w", exactErr)
	}
	if legacyErr != nil {
		return fmt.Errorf("persist legacy skip: %w", legacyErr)
	}
	return nil
}

// Get 是否跳过，获取跳过扫描的信息设置，带有缓存。电影就是具体的视频文件全路径，连续剧就是具体一集视频文件的全路径
func (s *ScanLogic) Get(videoType int, videoPath string) bool {
	// Exact-path overrides are checked first. Unlike the legacy movie UID, this
	// cannot affect sibling videos in the same directory; unlike the series UID,
	// it does not depend on successfully parsing SxxExx metadata.
	if s.getByUID(models.GenerateUID4VideoPath(videoPath), false) {
		return true
	}

	var uid string
	if videoType == 0 {
		// 电影
		uid = models.GenerateUID4Movie(videoPath)
	} else {
		// 电视剧
		skipInfo := models.NewSkipScanInfoBySeriesEx(videoPath, true)
		uid = skipInfo.UID
	}

	return s.getByUID(uid, true)
}

func (s *ScanLogic) getByUID(uid string, persistDefault bool) bool {
	value, found := s.scanLogicMap.Load(uid)
	if found {
		return value.(bool)
	}

	// Query without stateMu so a manual upload is never blocked behind a slow
	// read. The second cache check below linearizes a concurrent Set that
	// completes while this database miss is in flight.
	skipInfos, loadErr := s.loadByUID(uid)
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if value, found = s.scanLogicMap.Load(uid); found {
		return value.(bool)
	}
	if loadErr != nil {
		if s.l != nil {
			s.l.WithError(loadErr).Errorln("load scan skip decision", uid)
		}
		// Exact-path overrides fail closed so a transient database error cannot
		// let an automatic save overwrite a manual subtitle after restart. Legacy
		// cohort defaults retain their historical scan-on-error behavior.
		return !persistDefault
	}
	if len(skipInfos) < 1 {
		if persistDefault {
			// Legacy behavior records the default scan decision for cohort UIDs.
			skipInfo := models.NewSkipScanInfoByUID(uid, false)
			if persistErr := s.persist(skipInfo); persistErr != nil && s.l != nil {
				s.l.WithError(persistErr).Errorln("persist default scan decision", uid)
			}
		}
		s.scanLogicMap.Store(uid, false)
		return false
	}
	s.scanLogicMap.Store(uid, skipInfos[0].Skip)
	return skipInfos[0].Skip
}

func (s *ScanLogic) loadByUID(uid string) ([]models.SkipScanInfo, error) {
	if s.loadSkipInfosByUID != nil {
		return s.loadSkipInfosByUID(uid)
	}
	var skipInfos []models.SkipScanInfo
	result := dao.GetDb().Where("uid = ?", uid).Find(&skipInfos)
	return skipInfos, result.Error
}

func (s *ScanLogic) persist(skipInfo *models.SkipScanInfo) error {
	if s.persistSkipScanInfo != nil {
		return s.persistSkipScanInfo(skipInfo)
	}
	return dao.GetDb().Save(skipInfo).Error
}
