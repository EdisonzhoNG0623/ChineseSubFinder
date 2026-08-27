package settings

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/strcut_json"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
)

func TestNewSettings(t *testing.T) {
	configRoot := t.TempDir()
	fileName := filepath.Join(configRoot, "testfile.json")

	inSettings := Settings{
		UserInfo: &UserInfo{
			Username: "abcd",
			Password: "123456",
		},
		CommonSettings: &CommonSettings{
			ScanInterval:     "12h",
			Threads:          12,
			RunScanAtStartUp: true,
			MoviePaths:       []string{"aaa", "bbb"},
			SeriesPaths:      []string{"ccc", "ddd"},
		},
		AdvancedSettings: &AdvancedSettings{
			ProxySettings: &ProxySettings{
				UseProxy:                 true,
				LocalHttpProxyServerPort: "123",
			},
			DebugMode:                  true,
			SaveFullSeasonTmpSubtitles: true,
			SubTypePriority:            1,
			SubNameFormatter:           1,
			SaveMultiSub:               true,
			CustomVideoExts:            []string{"aaa", "bbb"},
			FixTimeLine:                true,
		},
		EmbySettings: &EmbySettings{
			Enable:                true,
			AddressUrl:            "123456",
			APIKey:                "api123",
			MaxRequestVideoNumber: 1000,
			SkipWatched:           true,
			MoviePathsMapping:     map[string]string{"aa": "123", "bb": "456"},
			SeriesPathsMapping:    map[string]string{"aab": "123", "bbc": "456"},
		},
		DeveloperSettings: &DeveloperSettings{
			BarkServerAddress: "bark",
		},
	}

	err := strcut_json.ToFile(fileName, inSettings)
	if err != nil {
		t.Fatal(err)
	}

	outSettings := NewSettings(configRoot)
	err = strcut_json.ToStruct(fileName, &outSettings)
	if err != nil {
		t.Fatal(err)
	}

	if reflect.DeepEqual(inSettings.UserInfo, outSettings.UserInfo) == false {
		t.Fatal("inSettings Write And Read Not The Same")
	}
}

func TestNormalizeAddsPublicSubtitleSourcesWithoutEnablingThem(t *testing.T) {
	s := NewSettings(t.TempDir())
	s.AdvancedSettings.SuppliersSettings.AnimeTosho = nil
	s.AdvancedSettings.SuppliersSettings.Addic7ed = nil
	s.Check()
	if s.SubtitleSources.AnimeToshoSettings.Enabled || s.SubtitleSources.Addic7edSettings.Enabled {
		t.Fatal("new public sources must remain opt-in")
	}
	if s.AdvancedSettings.SuppliersSettings.AnimeTosho == nil ||
		s.AdvancedSettings.SuppliersSettings.AnimeTosho.RootUrl != common.AnimeToshoRootURLDef {
		t.Fatal("AnimeTosho endpoint was not migrated")
	}
	if s.AdvancedSettings.SuppliersSettings.Addic7ed == nil ||
		s.AdvancedSettings.SuppliersSettings.Addic7ed.RootUrl != common.Addic7edRootURLDef {
		t.Fatal("Addic7ed endpoint was not migrated")
	}
}
