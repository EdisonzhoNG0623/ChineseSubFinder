package save_sub_helper

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/change_file_encode"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/chs_cht_changer"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ifaces"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/sub_timeline_fixer"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	timelineFixerArtifacts "github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_timeline_fixer"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/subparser"
	"github.com/sirupsen/logrus"
)

type SaveSubHelper struct {
	log                      *logrus.Logger
	SubFormatter             ifaces.ISubFormatter                         // 字幕格式化命名的实现
	subTimelineFixerHelperEx *sub_timeline_fixer.SubTimelineFixerHelperEx // 字幕时间轴校正
	videoWriteLocksMu        sync.Mutex
	videoWriteLocks          map[string]*videoWriteLock
}

type videoWriteLock struct {
	mutex sync.Mutex
	refs  int
}

// VideoWriteTransaction owns the complete write transaction for one video.
// Callers that need to update marker state or persist a manual override must do
// that work inside WithVideoWriteLock and use this writer to avoid re-entering
// the same non-reentrant lock.
type VideoWriteTransaction struct {
	helper            *SaveSubHelper
	videoFileFullPath string
}

func NewSaveSubHelper(log *logrus.Logger, subFormatter ifaces.ISubFormatter, subTimelineFixerHelperEx *sub_timeline_fixer.SubTimelineFixerHelperEx) *SaveSubHelper {
	return &SaveSubHelper{
		log: log, SubFormatter: subFormatter, subTimelineFixerHelperEx: subTimelineFixerHelperEx,
		videoWriteLocks: make(map[string]*videoWriteLock),
	}
}

// WriteSubFile2VideoPath 在前面需要进行语言的筛选、排序，这里仅仅是存储， extraSubPreName 这里传递是字幕的网站，有就认为是多字幕的存储。空就是单字幕，单字幕就可以setDefault
func (s *SaveSubHelper) WriteSubFile2VideoPath(videoFileFullPath string, finalSubFile subparser.FileInfo, extraSubPreName string, setDefault bool, skipExistFile bool) error {
	return s.WithVideoWriteLock(videoFileFullPath, func(writer *VideoWriteTransaction) error {
		return writer.WriteSubFile(finalSubFile, extraSubPreName, setDefault, skipExistFile)
	})
}

// WithVideoWriteLock serializes every visible subtitle/marker/backup mutation
// for one canonical video path while allowing unrelated videos to save in
// parallel. The ref-counted entry is removed after the last waiter leaves.
func (s *SaveSubHelper) WithVideoWriteLock(videoFileFullPath string, write func(*VideoWriteTransaction) error) error {
	release := s.acquireVideoWriteLock(videoFileFullPath)
	defer release()
	return write(&VideoWriteTransaction{helper: s, videoFileFullPath: videoFileFullPath})
}

// WriteSubFile writes one subtitle while its caller owns the video's complete
// transaction. It must only be called from the WithVideoWriteLock callback.
func (w *VideoWriteTransaction) WriteSubFile(finalSubFile subparser.FileInfo, extraSubPreName string,
	setDefault bool, skipExistFile bool) error {

	return w.helper.writeSubFile2VideoPathWithPipeline(
		w.videoFileFullPath, finalSubFile, extraSubPreName, setDefault, skipExistFile,
		func(stagedPath string) error {
			return w.helper.processStagedSubtitle(w.videoFileFullPath, stagedPath)
		},
	)
}

func (s *SaveSubHelper) acquireVideoWriteLock(videoFileFullPath string) func() {
	key := videoWriteLockKey(videoFileFullPath)

	s.videoWriteLocksMu.Lock()
	if s.videoWriteLocks == nil {
		s.videoWriteLocks = make(map[string]*videoWriteLock)
	}
	entry := s.videoWriteLocks[key]
	if entry == nil {
		entry = &videoWriteLock{}
		s.videoWriteLocks[key] = entry
	}
	entry.refs++
	s.videoWriteLocksMu.Unlock()

	entry.mutex.Lock()
	return func() {
		entry.mutex.Unlock()
		s.videoWriteLocksMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(s.videoWriteLocks, key)
		}
		s.videoWriteLocksMu.Unlock()
	}
}

// videoWriteLockKey follows the subtitle formatter namespace: videos with the
// same directory and basename but different media extensions publish to the
// same subtitle paths and therefore must share one transaction lock.
func videoWriteLockKey(videoFileFullPath string) string {
	canonicalPath := canonicalVideoWritePath(videoFileFullPath)
	return strings.TrimSuffix(canonicalPath, filepath.Ext(canonicalPath))
}

func canonicalVideoWritePath(videoFileFullPath string) string {
	key := filepath.Clean(videoFileFullPath)
	if absolutePath, err := filepath.Abs(key); err == nil {
		key = absolutePath
	}
	return key
}

func (s *SaveSubHelper) writeSubFile2VideoPathWithPipeline(videoFileFullPath string, finalSubFile subparser.FileInfo,
	extraSubPreName string, setDefault bool, skipExistFile bool, process func(string) error) error {

	defer s.log.Infoln("----------------------------------")
	videoRootPath := filepath.Dir(videoFileFullPath)
	subNewName, subNewNameWithDefault, _ := s.SubFormatter.GenerateMixSubName(videoFileFullPath, finalSubFile.Ext, finalSubFile.Lang, extraSubPreName)

	desSubFullPath := filepath.Join(videoRootPath, subNewName)
	oldNonDefaultPath := ""
	if setDefault == true {
		// Keep the prior non-default subtitle until the new default subtitle has
		// completed every post-processing step and is durably installed.
		oldNonDefaultPath = desSubFullPath
		desSubFullPath = filepath.Join(videoRootPath, subNewNameWithDefault)
	}

	if skipExistFile == true {
		// 需要判断文件是否存在在，有则跳过
		if pkg.IsFile(desSubFullPath) == true {
			s.log.Infoln("OrgSubName:", finalSubFile.Name)
			s.log.Infoln("Sub Skip DownAt:", desSubFullPath)
			return nil
		}
	}
	// Build and post-process a sibling staging file. The visible subtitle is
	// replaced only after the complete pipeline succeeds.
	err := writeSubtitleFileWithPipeline(desSubFullPath, finalSubFile.Data, process, func(publishErr error) {
		s.log.WithError(publishErr).Warnln("publish processed subtitle backup", desSubFullPath+timelineFixerArtifacts.BackUpExt)
	})
	if err != nil {
		return err
	}
	s.log.Infoln("----------------------------------")
	s.log.Infoln("OrgSubName:", finalSubFile.Name)
	s.log.Infoln("SubDownAt:", desSubFullPath)
	if oldNonDefaultPath != "" && oldNonDefaultPath != desSubFullPath {
		// Both entries belong to the superseded non-default install. Leaving its
		// timeline backup behind would let a later Restore() overwrite the new
		// default subtitle with stale contents. The new install is already
		// authoritative, so cleanup failures are warnings rather than save retries.
		for _, supersededPath := range []string{
			oldNonDefaultPath,
			oldNonDefaultPath + timelineFixerArtifacts.BackUpExt,
		} {
			if removeErr := removeSupersededSubtitle(supersededPath); removeErr != nil {
				s.log.WithError(removeErr).Warnln("remove superseded non-default subtitle", supersededPath)
			}
		}
	}
	return nil
}

func removeSupersededSubtitle(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("superseded subtitle is not a regular file: %s", path)
	}
	return os.Remove(path)
}

func (s *SaveSubHelper) processStagedSubtitle(videoFileFullPath, stagedSubPath string) error {
	var err error
	// 然后还需要判断是否需要校正字幕的时间轴
	if settings.Get().AdvancedSettings.FixTimeLine == true {
		err = s.subTimelineFixerHelperEx.Process(videoFileFullPath, stagedSubPath)
		if err != nil {
			return err
		}
	}
	// 判断是否需要转换字幕的编码
	if settings.Get().ExperimentalFunction.AutoChangeSubEncode.Enable == true {
		s.log.Infoln("----------------------------------")
		s.log.Infoln("change_file_encode to", settings.Get().ExperimentalFunction.AutoChangeSubEncode.GetDesEncodeType())
		err = change_file_encode.Process(stagedSubPath, settings.Get().ExperimentalFunction.AutoChangeSubEncode.DesEncodeType)
		if err != nil {
			return err
		}
	}

	// 判断是否需要进行简繁互转
	// 一定得是 UTF-8 才能够执行简繁转换
	// 测试了先转 UTF-8 进行简繁转换然后再转 GBK，有些时候会出错，所以还是不支持这样先
	if settings.Get().ExperimentalFunction.AutoChangeSubEncode.Enable == true &&
		settings.Get().ExperimentalFunction.AutoChangeSubEncode.DesEncodeType == 0 &&
		settings.Get().ExperimentalFunction.ChsChtChanger.Enable == true {
		s.log.Infoln("----------------------------------")
		s.log.Infoln("chs_cht_changer to", settings.Get().ExperimentalFunction.ChsChtChanger.GetDesChineseLanguageTypeString())
		err = chs_cht_changer.Process(stagedSubPath, settings.Get().ExperimentalFunction.ChsChtChanger.DesChineseLanguageType)
		if err != nil {
			return err
		}
	}

	return nil
}

// writeSubtitleFileAtomically writes a sibling temporary file and renames it
// over the destination. Replacing the directory entry avoids opening an
// existing subtitle for truncation, which can fail when the media directory is
// writable but the old subtitle belongs to a different UID.
func writeSubtitleFileAtomically(path string, data []byte) error {
	return writeSubtitleFileWithPipeline(path, data, nil, nil)
}

func writeSubtitleFileWithPipeline(path string, data []byte, process func(string) error, warn func(error)) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}

	temp, err := createSubtitleTempFile(dir, filepath.Ext(path))
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		cleanupSubtitleStaging(tempPath)
	}()

	written, err := temp.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	createdInfo, err := os.Stat(tempPath)
	if err != nil {
		return err
	}
	if process != nil {
		if err = process(tempPath); err != nil {
			return err
		}
	}
	if err = installStagedSubtitle(tempPath, path, createdInfo.Mode().Perm()); err != nil {
		return err
	}
	if err = publishStagedTimelineBackup(tempPath, path); err != nil && warn != nil {
		// The processed subtitle is already committed. A backup publication
		// failure must not turn that successful save into a retry that may replace
		// the installed subtitle again.
		warn(err)
	}
	return nil
}

func publishStagedTimelineBackup(stagedPath, destinationPath string) error {
	stagedBackupPath := stagedPath + timelineFixerArtifacts.BackUpExt
	info, err := os.Stat(stagedBackupPath)
	if errors.Is(err, os.ErrNotExist) {
		// The newly installed subtitle was not timeline-adjusted this time. A
		// backup left by an older install now belongs to different subtitle
		// contents and must not remain available to Restore(). Main subtitle
		// publication has already succeeded, so cleanup remains best-effort to
		// avoid turning a successful save into a retry.
		destinationBackupPath := destinationPath + timelineFixerArtifacts.BackUpExt
		destinationInfo, destinationErr := os.Lstat(destinationBackupPath)
		if errors.Is(destinationErr, os.ErrNotExist) {
			return nil
		}
		if destinationErr != nil {
			return fmt.Errorf("inspect stale timeline backup: %w", destinationErr)
		}
		if !destinationInfo.Mode().IsRegular() && destinationInfo.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("stale timeline backup is not a regular file: %s", destinationBackupPath)
		}
		if removeErr := os.Remove(destinationBackupPath); removeErr != nil {
			return fmt.Errorf("remove stale timeline backup: %w", removeErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect staged timeline backup: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("staged timeline backup is not a regular file: %s", stagedBackupPath)
	}
	if err = installStagedSubtitle(stagedBackupPath, destinationPath+timelineFixerArtifacts.BackUpExt, info.Mode().Perm()); err != nil {
		return fmt.Errorf("install timeline backup: %w", err)
	}
	return nil
}

func installStagedSubtitle(stagedPath, destinationPath string, newFileMode os.FileMode) error {
	replacementMode := newFileMode
	if info, err := os.Lstat(destinationPath); err == nil {
		if info.Mode().IsRegular() {
			// Preserve useful sharing permissions while ensuring optional future
			// conversions can write the process-owned replacement.
			replacementMode = info.Mode().Perm() | 0o600
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Chmod(stagedPath, replacementMode); err != nil {
		return err
	}
	staged, err := os.Open(stagedPath)
	if err != nil {
		return err
	}
	if err = staged.Sync(); err != nil {
		_ = staged.Close()
		return err
	}
	if err = staged.Close(); err != nil {
		return err
	}
	return os.Rename(stagedPath, destinationPath)
}

func cleanupSubtitleStaging(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + timelineFixerArtifacts.TmpExt)
	_ = os.Remove(path + timelineFixerArtifacts.BackUpExt)
}

// createSubtitleTempFile creates a collision-resistant sibling using the
// same 0666-and-process-umask permission semantics as os.Create. O_EXCL keeps
// an attacker or another worker from redirecting the temporary path.
func createSubtitleTempFile(dir, extension string) (*os.File, error) {
	const maxAttempts = 10
	var nonce [16]byte
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		tempPath := filepath.Join(dir, ".csf-subtitle-"+hex.EncodeToString(nonce[:])+extension)
		temp, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if err == nil {
			return temp, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("create unique subtitle temporary file: %w", fs.ErrExist)
}
