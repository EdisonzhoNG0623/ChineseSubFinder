package subhd

import (
	"bytes"
	"os"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/sirupsen/logrus"
)

func TestLiveSubHDDownload(t *testing.T) {
	if os.Getenv("CSF_SUBHD_LIVE_TEST") != "1" {
		t.Skip("set CSF_SUBHD_LIVE_TEST=1 to exercise the current SubHD download flow")
	}

	settings.SetConfigRootPath(t.TempDir())
	appSettings := settings.Get()
	appSettings.AdvancedSettings.SuppliersSettings.SubHD.RootUrl = "https://subhd.tv"

	log := logrus.New()
	supplier := &Supplier{log: log}
	subInfo, err := supplier.DownFile(nil, "/a/JrRjGa", 1, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if subInfo == nil || len(subInfo.Data) < 100 {
		t.Fatalf("downloaded subtitle is empty: %+v", subInfo)
	}
	prefixLength := len(subInfo.Data)
	if prefixLength > 512 {
		prefixLength = 512
	}
	if bytes.Contains(bytes.ToLower(subInfo.Data[:prefixLength]), []byte("<html")) {
		t.Fatal("download returned an HTML block page instead of a subtitle archive")
	}
	t.Logf("downloaded %s (%d bytes)", subInfo.Name, len(subInfo.Data))
}
