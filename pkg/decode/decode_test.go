package decode

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/unit_test_helper"
)

func TestGetSeriesDirRootFPathFindsNearestScrapedRoot(t *testing.T) {
	root := t.TempDir()
	category := filepath.Join(root, "Anime", "日番")
	seriesRoot := filepath.Join(category, "Example Show (2026)")
	episodeDir := filepath.Join(seriesRoot, "Specials", "Season 01")
	if err := os.MkdirAll(episodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seriesRoot, "TVSHOW.NFO"), []byte("<tvshow/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(episodeDir, "Example Show - S01E01.mkv")
	if err := os.WriteFile(videoPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := GetSeriesDirRootFPath(videoPath); got != seriesRoot {
		t.Fatalf("series root = %q, want %q", got, seriesRoot)
	}
}

func TestGetSeriesDirRootFPathRejectsCategoryDirectory(t *testing.T) {
	root := t.TempDir()
	episodeDir := filepath.Join(root, "Anime", "日番", "Unknown Show", "Season 01")
	if err := os.MkdirAll(episodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(episodeDir, "Unknown Show - S01E01.mkv")
	if err := os.WriteFile(videoPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := GetSeriesDirRootFPath(videoPath); got != "" {
		t.Fatalf("series root = %q, want empty", got)
	}
}

func TestGetImdbAndYearMovieXml(t *testing.T) {

	rootDir := unit_test_helper.GetTestDataResourceRootPath([]string{"movies", "Army of the Dead (2021)"}, 4, false)

	wantid := "tt0993840"
	wantyear := "2021"
	dirPth := filepath.Join(rootDir, "movie.xml")
	imdbInfo, err := getVideoNfoInfoFromMovieXml(dirPth)
	if err != nil {
		t.Error(err)
	}
	if imdbInfo.ImdbId != wantid {
		t.Errorf("got = %v, want %v", imdbInfo.ImdbId, wantid)
	}
	if imdbInfo.Year != wantyear {
		t.Errorf("got = %v, want %v", imdbInfo.Year, wantyear)
	}
}

func Test_getImdbAndYearNfo(t *testing.T) {
	type args struct {
		nfoFilePath string
		rootKey     string
	}
	tests := []struct {
		name    string
		args    args
		want    types.VideoNfoInfo
		wantErr bool
	}{
		{
			name: "Army of the Dead (2021) WEBDL-1080p.nfo", args: args{
				nfoFilePath: filepath.Join(unit_test_helper.GetTestDataResourceRootPath([]string{"movies", "Army of the Dead (2021)"}, 4, false), "Army of the Dead (2021) WEBDL-1080p.nfo"),
				rootKey:     "movie",
			},
			want: types.VideoNfoInfo{
				ImdbId:      "tt0993840",
				Title:       "活死人军团",
				Year:        "2021",
				ReleaseDate: "2021-05-13",
			},
			wantErr: false,
		},
		{
			name: "tvshow_00 (2).nfo", args: args{
				nfoFilePath: filepath.Join(unit_test_helper.GetTestDataResourceRootPath([]string{"nfo_files", "tvshow"}, 4, false), "tvshow_00 (2).nfo"),
				rootKey:     "tvshow",
			},
			want: types.VideoNfoInfo{
				ImdbId:      "tt0346314",
				Title:       "Ghost in the Shell: Stand Alone Complex",
				ReleaseDate: "2002-10-01",
			},
			wantErr: false,
		},
		{
			name: "tvshow_00 (3).nfo", args: args{
				nfoFilePath: filepath.Join(unit_test_helper.GetTestDataResourceRootPath([]string{"nfo_files", "tvshow"}, 4, false), "tvshow_00 (3).nfo"),
				rootKey:     "tvshow",
			},
			want: types.VideoNfoInfo{
				ImdbId:      "tt1856010",
				Title:       "House of Cards (US)",
				ReleaseDate: "2013-02-01",
			},
			wantErr: false,
		},
		{
			name: "tvshow_00 (63).nfo", args: args{
				nfoFilePath: filepath.Join(unit_test_helper.GetTestDataResourceRootPath([]string{"nfo_files", "tvshow"}, 3, false), "tvshow_00 (63).nfo"),
				rootKey:     "tvshow",
			},
			want: types.VideoNfoInfo{
				ImdbId:      "tt3581920",
				TVdbId:      "392256",
				Title:       "The Last of Us",
				ReleaseDate: "2023-01-15",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getVideoNfoInfo(tt.args.nfoFilePath, tt.args.rootKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("getVideoNfoInfo() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getVideoNfoInfo() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetSeasonAndEpisodeFromFileName(t *testing.T) {
	str := `杀死伊芙 第二季(-简繁英双语字幕-FIX字幕侠)Killing.Eve.S02.Do.You.Know.How.to.Dispose.of.a.Body.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb.rar`
	b, s, e, err := GetSeasonAndEpisodeFromSubFileName(str)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("\n\n%t\t S%dE%d\n", b, s, e)
}

func TestGetSeasonAndEpisodeFromFileNameBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		fileName    string
		wantSeason  int
		wantEpisode int
		wantPack    bool
	}{
		{name: "token at end", fileName: "Show.S04E35", wantSeason: 4, wantEpisode: 35},
		{name: "underscore separators", fileName: "Show_S04_E36.ass", wantSeason: 4, wantEpisode: 36},
		{name: "bracket separators", fileName: "[S04E37] Show.ass", wantSeason: 4, wantEpisode: 37},
		{name: "season package at end", fileName: "Show S04", wantSeason: 4, wantPack: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pack, season, episode, err := GetSeasonAndEpisodeFromSubFileName(test.fileName)
			if err != nil {
				t.Fatal(err)
			}
			if pack != test.wantPack || season != test.wantSeason || episode != test.wantEpisode {
				t.Fatalf("parsed (%t,%d,%d), want (%t,%d,%d)", pack, season, episode, test.wantPack, test.wantSeason, test.wantEpisode)
			}
		})
	}
}

func TestGetNumber2Float(t *testing.T) {
	testString := "asd&^%1998.2jh aweo "
	outNumber, err := GetNumber2Float(testString)
	if err != nil {
		t.Error(err)
	}
	if outNumber != 1998.2 {
		t.Error("not the same")
	}
}

func TestGetNumber2int(t *testing.T) {

	testString := "asd&^%1998jh aweo "
	outNumber, err := GetNumber2int(testString)
	if err != nil {
		t.Error(err)
	}
	if outNumber != 1998 {
		t.Error("not the same")
	}
}

func TestGetImdbInfo4SeriesDir(t *testing.T) {

	type args struct {
		seriesDir string
	}
	tests := []struct {
		name    string
		args    args
		want    types.VideoNfoInfo
		wantErr bool
	}{
		{
			name: "Loki",
			args: args{seriesDir: unit_test_helper.GetTestDataResourceRootPath([]string{"series", "Loki"}, 4, false)},
			want: types.VideoNfoInfo{
				ImdbId:      "tt9140554",
				Title:       "Loki",
				ReleaseDate: "2021-06-09",
			},
			wantErr: false,
		},
		{
			name: "辛普森一家",
			args: args{seriesDir: unit_test_helper.GetTestDataResourceRootPath([]string{"series", "辛普森一家"}, 4, false)},
			want: types.VideoNfoInfo{
				ImdbId:      "tt0096697",
				Title:       "The Simpsons",
				ReleaseDate: "1989-12-17",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetVideoNfoInfo4SeriesDir(tt.args.seriesDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetVideoNfoInfo4SeriesDir() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetVideoNfoInfo4SeriesDir() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsFakeBDMVWorked(t *testing.T) {

	rootDir := unit_test_helper.GetTestDataResourceRootPath([]string{"movies", "失控玩家 (2021)"}, 4, false)

	bok, dbmvFPath, _ := IsFakeBDMVWorked(filepath.Join(rootDir, "失控玩家 (2021).mp4"))
	if bok == false {
		t.Fatal("IsFakeBDMVWorked error")
	}
	println(dbmvFPath)
}

func TestGetImdbInfo4Movie(t *testing.T) {
	//type args struct {
	//	movieFileFullPath string
	//}
	//tests := []struct {
	//	name    string
	//	args    args
	//	want    types.VideoNfoInfo
	//	wantErr bool
	//}{
	//	{name: "00", args: args{
	//		movieFileFullPath: "X:\\电影\\Death on the Nile (2022)\\Death on the Nile (2022) Bluray-1080p.mkv",
	//	}},
	//}
	//for _, tt := range tests {
	//	t.Run(tt.name, func(t *testing.T) {
	//		got, err := GetVideoNfoInfo4Movie(tt.args.movieFileFullPath)
	//		if (err != nil) != tt.wantErr {
	//			t.Errorf("GetVideoNfoInfo4Movie() error = %v, wantErr %v", err, tt.wantErr)
	//			return
	//		}
	//		if !reflect.DeepEqual(got, tt.want) {
	//			t.Errorf("GetVideoNfoInfo4Movie() got = %v, want %v", got, tt.want)
	//		}
	//	})
	//}
}
