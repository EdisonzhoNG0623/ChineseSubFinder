package cache_center

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/cache_center/models"
)

func (c *CacheCenter) TaskQueueClear() error {

	// 没有必要删除 DB 中的数据，直接删除外部的缓存文件即可
	err := pkg.ClearFolder(c.taskQueueSaveRootPath)
	if err != nil {
		return err
	}
	return nil
}

func (c *CacheCenter) TaskQueueSave(taskPriority int, taskQueueBytes []byte) error {
	defer c.locker.Unlock()
	c.locker.Lock()

	var taskQueues []models.TaskQueueInfo
	if result := c.db.Where("priority = ?", taskPriority).Find(&taskQueues); result.Error != nil {
		return result.Error
	}
	// Write and fsync a sibling temporary file first. Replacing the final
	// snapshot with rename keeps the previous valid JSON intact across process
	// crashes, host power loss and short writes.
	saveFPath := filepath.Join(c.taskQueueSaveRootPath, fmt.Sprintf("%d", taskPriority)+".tq")
	if err := os.MkdirAll(c.taskQueueSaveRootPath, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(c.taskQueueSaveRootPath, fmt.Sprintf(".%d.tq-*", taskPriority))
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keepTemp := true
	defer func() {
		_ = temp.Close()
		if keepTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err = temp.Chmod(0o600); err != nil {
		return err
	}
	written, err := temp.Write(taskQueueBytes)
	if err != nil {
		return err
	}
	if written != len(taskQueueBytes) {
		return io.ErrShortWrite
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	relPath, err := filepath.Rel(c.taskQueueSaveRootPath, saveFPath)
	if err != nil {
		return err
	}

	if len(taskQueues) == 0 {
		// 不存在，需要新建
		nowTaskQueue := models.TaskQueueInfo{
			Priority: taskPriority,
			RelPath:  relPath,
		}
		if result := c.db.Save(&nowTaskQueue); result.Error != nil {
			return result.Error
		}
	} else {
		// 存在，需要更新
		taskQueues[0].RelPath = relPath
		if result := c.db.Save(&taskQueues[0]); result.Error != nil {
			return result.Error
		}
	}
	if err = os.Rename(tempPath, saveFPath); err != nil {
		return err
	}
	keepTemp = false
	// Rename is the commit point: the new snapshot is already visible. A
	// directory fsync failure must not be returned as a transactional failure,
	// otherwise queue callers may roll memory back while disk contains the new
	// state. Some NAS/FUSE filesystems report EINVAL or ENOTSUP for directory
	// fsync; other post-commit errors are warnings for the same reason.
	directory, err := os.Open(c.taskQueueSaveRootPath)
	if err != nil {
		c.Log.WithError(err).Warn("task queue snapshot committed but snapshot directory could not be opened for fsync")
		return nil
	}
	if err = directory.Sync(); err != nil {
		if isUnsupportedDirectorySyncError(err) {
			c.Log.WithError(err).Debug("task queue snapshot directory does not support fsync")
		} else {
			c.Log.WithError(err).Warn("task queue snapshot committed but snapshot directory fsync failed")
		}
	}
	if err = directory.Close(); err != nil {
		c.Log.WithError(err).Warn("task queue snapshot committed but snapshot directory close failed")
	}

	return nil
}

func isUnsupportedDirectorySyncError(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP)
}

func (c *CacheCenter) TaskQueueRead() (map[int][]byte, error) {
	defer c.locker.Unlock()
	c.locker.Lock()

	var taskQueues []models.TaskQueueInfo
	if result := c.db.Find(&taskQueues); result.Error != nil {
		return nil, result.Error
	}

	outTaskQueueBytes := make(map[int][]byte, 0)
	for _, taskQueue := range taskQueues {

		oneTaskQueueFPath := filepath.Join(c.taskQueueSaveRootPath, taskQueue.RelPath)
		if pkg.IsFile(oneTaskQueueFPath) == false {
			continue
		}
		bytes, err := os.ReadFile(oneTaskQueueFPath)
		if err != nil {
			return nil, err
		}

		outTaskQueueBytes[taskQueue.Priority] = bytes
	}

	return outTaskQueueBytes, nil
}
