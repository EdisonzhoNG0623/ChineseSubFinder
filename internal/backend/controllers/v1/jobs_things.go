package v1

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"

	backend2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/emby"
	task_queue3 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/task_queue"

	task_queue2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/task_queue"
	"github.com/gin-gonic/gin"
)

var errDownloadingPriorityChange = errors.New("cannot change priority while a job is downloading; reset or ignore it first")

const maxJobLogIDLength = 128

func (cb *ControllerBase) JobsListHandler(c *gin.Context) {
	var err error
	defer func() {
		// 统一的异常处理
		cb.ErrorProcess(c, "JobsListHandler", err)
	}()

	bok, allJobs, err := cb.cronHelper.DownloadQueue.GetAllJobs()
	if err != nil {
		return
	}

	if bok == false {
		c.JSON(http.StatusOK, backend2.ReplyAllJobs{
			AllJobs: make([]task_queue3.OneJob, 0),
		})
		return
	}

	c.JSON(http.StatusOK, backend2.ReplyAllJobs{
		AllJobs: allJobs,
	})
}

func (cb *ControllerBase) ChangeJobStatusHandler(c *gin.Context) {
	desJobStatus := backend2.ReqChangeJobStatus{}
	if err := c.ShouldBindJSON(&desJobStatus); err != nil {
		c.JSON(http.StatusBadRequest, backend2.ReplyCommon{Message: "invalid request: " + err.Error()})
		return
	}
	if strings.TrimSpace(desJobStatus.Id) == "" {
		c.JSON(http.StatusBadRequest, backend2.ReplyCommon{Message: "job id is required"})
		return
	}

	bok, nowOneJob := cb.cronHelper.DownloadQueue.GetOneJobByID(desJobStatus.Id)
	if bok == false {
		c.JSON(http.StatusNotFound, backend2.ReplyCommon{Message: "job not found"})
		return
	}
	expectedRevision := nowOneJob.StateRevision

	changed, err := applyRequestedJobChanges(&nowOneJob, desJobStatus)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errDownloadingPriorityChange) {
			status = http.StatusConflict
		}
		c.JSON(status, backend2.ReplyCommon{Message: err.Error()})
		return
	}
	if !changed {
		c.JSON(http.StatusOK, backend2.ReplyCommon{Message: "ok"})
		return
	}

	bok, err = cb.cronHelper.DownloadQueue.UpdateIfRevision(nowOneJob, expectedRevision)
	if err != nil {
		cb.log.Errorln("ChangeJobStatusHandler", err)
		c.JSON(http.StatusInternalServerError, backend2.ReplyCommon{Message: err.Error()})
		return
	}

	if bok == false {
		c.JSON(http.StatusConflict, backend2.ReplyCommon{Message: "job changed before the update was applied"})
		return
	}

	c.JSON(http.StatusOK, backend2.ReplyCommon{Message: "ok"})
}

func applyRequestedJobChanges(job *task_queue3.OneJob, request backend2.ReqChangeJobStatus) (bool, error) {
	if job == nil {
		return false, fmt.Errorf("job is required")
	}
	if request.TaskPriority == nil && request.JobStatus == nil {
		return false, fmt.Errorf("no job changes requested")
	}

	updated := *job
	if request.TaskPriority != nil {
		priority, ok := requestedTaskPriority(*request.TaskPriority)
		if !ok {
			return false, fmt.Errorf("unsupported task priority %q", *request.TaskPriority)
		}
		if job.JobStatus == task_queue3.Downloading && request.JobStatus == nil && priority != job.TaskPriority {
			return false, errDownloadingPriorityChange
		}
		updated.TaskPriority = priority
	}
	if request.JobStatus != nil {
		switch *request.JobStatus {
		case task_queue3.Waiting:
			updated.JobStatus = task_queue3.Waiting
			// A user-triggered Waiting transition bypasses backoff exactly once.
			updated.ForceRun = true
			updated.NotBeforeTime = emby.Time{}
		case task_queue3.Ignore:
			updated.JobStatus = task_queue3.Ignore
			updated.ForceRun = false
		default:
			return false, fmt.Errorf("unsupported job status %d", *request.JobStatus)
		}
	}

	changed := updated.TaskPriority != job.TaskPriority ||
		updated.JobStatus != job.JobStatus ||
		updated.ForceRun != job.ForceRun ||
		!time.Time(updated.NotBeforeTime).Equal(time.Time(job.NotBeforeTime))
	if changed {
		*job = updated
	}
	return changed, nil
}

func requestedTaskPriority(value string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "high", "高":
		return task_queue2.HighTaskPriorityLevel, true
	case "middle", "mddile", "中":
		return task_queue2.DefaultTaskPriorityLevel, true
	case "low", "低":
		return task_queue2.LowTaskPriorityLevel, true
	default:
		return 0, false
	}
}

func (cb *ControllerBase) JobLogHandler(c *gin.Context) {
	var err error
	defer func() {
		// 统一的异常处理
		cb.ErrorProcess(c, "JobLogHandler", err)
	}()

	reqJobLog := backend2.ReqJobLog{}
	err = c.ShouldBindJSON(&reqJobLog)
	if err != nil {
		return
	}

	pathRoot := filepath.Join(pkg.ConfigRootDirFPath(), "Logs")
	fileFPath, validJobID := jobLogFilePath(pathRoot, reqJobLog.Id)
	if !validJobID {
		c.JSON(http.StatusBadRequest, backend2.ReplyCommon{Message: "invalid job id"})
		return
	}
	if pkg.IsFile(fileFPath) == true {
		// 存在
		// 一行一行的读取文件
		var fi *os.File
		fi, err = os.Open(fileFPath)
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return
		}
		defer fi.Close()

		ReplyJobLog := backend2.ReplyJobLog{}
		ReplyJobLog.OneLine = make([]string, 0)
		br := bufio.NewReader(fi)
		for {
			a, _, c := br.ReadLine()
			if c == io.EOF {
				break
			}
			ReplyJobLog.OneLine = append(ReplyJobLog.OneLine, string(a))
		}

		c.JSON(http.StatusOK, ReplyJobLog)
		return
	} else {
		// 不存在
		c.JSON(http.StatusOK, backend2.ReplyCommon{Message: "job log not found"})
		return
	}
}

func jobLogFilePath(pathRoot, jobID string) (string, bool) {
	if jobID == "" || len(jobID) > maxJobLogIDLength {
		return "", false
	}
	for i := 0; i < len(jobID); i++ {
		char := jobID[i]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return "", false
	}

	cleanRoot := filepath.Clean(pathRoot)
	filePath := filepath.Join(cleanRoot, common.OnceLogPrefix+jobID+".log")
	relativePath, err := filepath.Rel(cleanRoot, filePath)
	if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filePath, true
}
