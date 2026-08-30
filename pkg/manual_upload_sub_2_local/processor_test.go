package manual_upload_sub_2_local

import (
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/save_sub_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/sub_formatter/normal"
)

func TestNewManualUploadSub2Local(t *testing.T) {

	logger := log_helper.GetLogger4Tester()
	saveHelper := save_sub_helper.NewSaveSubHelper(logger, normal.NewFormatter(logger), nil)
	processor := NewManualUploadSub2Local(logger, saveHelper, nil)
	if processor == nil {
		t.Fatal("constructor returned nil")
	}
}
