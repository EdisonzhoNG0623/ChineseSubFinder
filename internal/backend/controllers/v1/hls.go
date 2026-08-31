package v1

import (
	b64 "encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/backend/middle"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
	backend2 "github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	"github.com/gin-gonic/gin"
)

const (
	hlsPlaylistContentType = "application/vnd.apple.mpegurl"
	hlsSegmentContentType  = "video/mp2t"
	hlsPreviewResolution   = int64(720)
)

// HlsPlaylist 获取 m3u8 列表
func (cb *ControllerBase) HlsPlaylist(c *gin.Context) {

	var err error
	defer func() {
		// 统一的异常处理
		cb.ErrorProcess(c, "HlsPlaylist", err)
	}()

	videoFPathBase64 := c.Param("videofpathbase64")
	// base64 解码
	videoFPathUrlEncodeStr, err := b64.StdEncoding.DecodeString(videoFPathBase64)
	if err != nil {
		return
	}
	// url 解码
	videoFPath, err := url.QueryUnescape(string(videoFPathUrlEncodeStr))
	if err != nil {
		return
	}

	// 暂时不支持蓝光的预览
	if pkg.IsFile(videoFPath) == false {
		bok, _, _ := decode.IsFakeBDMVWorked(videoFPath)
		if bok == true {
			c.JSON(http.StatusOK, backend2.ReplyCommon{Message: "not support blu-ray preview"})
			return
		} else {
			c.JSON(http.StatusOK, backend2.ReplyCommon{Message: "video file not found"})
			return
		}
	}

	// segments/720/0/videofpathbase64
	middle.IssueHLSPlaylistTicket(c, videoFPathBase64)
	template := hlsSegmentURLTemplate(videoFPathBase64, middle.NewHLSStreamTicket(c, videoFPathBase64))
	setHLSPlaylistContentType(c)
	err = cb.hslCenter.WritePlaylist(template, videoFPath, c.Writer)
	if err != nil {
		return
	}
}

func hlsSegmentURLTemplate(videoFPathBase64, streamTicket string) string {
	// The playlist lives below /preview/playlist/:video. Resolve segments one
	// level above "playlist" so reverse-proxy prefixes stay intact while the
	// request still reaches /preview/segments/:resolution/:segment/:video.
	template := fmt.Sprintf("../segments/{{.Resolution}}/{{.Segment}}/%v", videoFPathBase64)
	if streamTicket != "" {
		template += "?" + url.Values{middle.HLSStreamTicketQueryParam: []string{streamTicket}}.Encode()
	}
	return template
}

// HlsSegment 获取具体一个 ts 文件
func (cb *ControllerBase) HlsSegment(c *gin.Context) {

	var err error
	defer func() {
		// 统一的异常处理
		cb.ErrorProcess(c, "HlsSegment", err)
	}()

	resolution := c.Param("resolution")
	segment := c.Param("segment")
	videoFPathBase64 := c.Param("videofpathbase64")
	// base64 解码
	videoFPathUrlEncodeStr, err := b64.StdEncoding.DecodeString(videoFPathBase64)
	if err != nil {
		return
	}
	// url 解码
	videoFPath, err := url.QueryUnescape(string(videoFPathUrlEncodeStr))
	if err != nil {
		return
	}
	segmentInt64, resolutionInt64, err := parseHLSSegmentParameters(segment, resolution)
	if err != nil {
		c.JSON(http.StatusBadRequest, backend2.ReplyCommon{Message: "invalid HLS segment request"})
		err = nil
		return
	}
	setHLSSegmentContentType(c)
	err = cb.hslCenter.WriteSegment(videoFPath, segmentInt64, resolutionInt64, c.Writer)
	if err != nil {
		return
	}
}

func parseHLSSegmentParameters(segment, resolution string) (int64, int64, error) {
	segmentIndex, err := strconv.ParseInt(segment, 10, 64)
	if err != nil || segmentIndex < 0 {
		return 0, 0, errors.New("invalid segment index")
	}
	requestedResolution, err := strconv.ParseInt(resolution, 10, 64)
	if err != nil || requestedResolution != hlsPreviewResolution {
		return 0, 0, errors.New("unsupported resolution")
	}
	return segmentIndex, requestedResolution, nil
}

func setHLSPlaylistContentType(context *gin.Context) {
	context.Header("Content-Type", hlsPlaylistContentType)
}

func setHLSSegmentContentType(context *gin.Context) {
	context.Header("Content-Type", hlsSegmentContentType)
}
