package animetosho

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ulikunitz/xz"
)

func initSettings(t *testing.T) {
	t.Helper()
	settings.SetConfigRootPath(t.TempDir())
	settings.Get().SubtitleSources.AnimeToshoSettings.Enabled = true
}

func TestMatchingFeedItemsUsesStrictTitleAndEpisodeIdentity(t *testing.T) {
	items := []feedItem{
		{ID: 1, TorrentName: "Frieren.Beyond.Journeys.End.S02E03.1080p"},
		{ID: 2, TorrentName: "Other.Show.S02E03.1080p"},
		{ID: 3, TorrentName: "Frieren.Beyond.Journeys.End.031.1080p"},
		{ID: 4, TorrentName: "Frieren.Beyond.Journeys.End.030.1080p"},
	}
	episode := series.EpisodeInfo{Season: 2, Episode: 3, AbsoluteEpisode: 31}
	matched := matchingFeedItems(items, []string{"Frieren Beyond Journey's End"}, episode)
	if len(matched) != 2 || matched[0].ID != 3 || matched[1].ID != 1 {
		t.Fatalf("unexpected matches: %+v", matched)
	}
}

func TestSelectChineseAttachmentsPrefersSimplified(t *testing.T) {
	traditional := attachmentInfo{ID: 2, Type: "subtitle", Size: 100}
	traditional.Info.Codec, traditional.Info.Lang, traditional.Info.Name, traditional.Info.TrackNum = "ASS", "chi", "Traditional", 2
	simplified := attachmentInfo{ID: 1, Type: "subtitle", Size: 100}
	simplified.Info.Codec, simplified.Info.Lang, simplified.Info.Name, simplified.Info.TrackNum = "ASS", "chi", "Simplified", 1
	items := selectChineseAttachments([]attachmentInfo{traditional, simplified})
	if len(items) != 2 || items[0].ID != 1 {
		t.Fatalf("unexpected attachment order: %+v", items)
	}
}

func TestSelectEpisodeAttachmentsDoesNotMixBatchFiles(t *testing.T) {
	first := attachmentInfo{ID: 1, Type: "subtitle", Size: 100, SourceFilename: "Show.S01E01.mkv"}
	first.Info.Codec, first.Info.Lang, first.Info.TrackNum = "ASS", "chi", 1
	second := attachmentInfo{ID: 2, Type: "subtitle", Size: 100, SourceFilename: "Show.S01E02.mkv"}
	second.Info.Codec, second.Info.Lang, second.Info.TrackNum = "ASS", "chi", 1
	detail := &detailResponse{Files: []detailFile{{Filename: first.SourceFilename}, {Filename: second.SourceFilename}}, Attachments: []attachmentInfo{first, second}}
	items := selectEpisodeAttachments(detail, series.EpisodeInfo{Season: 1, Episode: 2})
	if len(items) != 1 || items[0].ID != 2 {
		t.Fatalf("batch attachments mixed episodes: %+v", items)
	}
}

func TestDetailModelAcceptsAttachmentsNestedUnderFile(t *testing.T) {
	var detail detailResponse
	data := []byte(`{"files":[{"filename":"Show.S01E03.mkv","attachments":[{"id":2902300,"type":"subtitle","size":27344,"info":{"codec":"ASS","lang":"chi","name":"Simplified","tracknum":25}}]}]}`)
	if err := json.Unmarshal(data, &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Files) != 1 || len(detail.Files[0].Attachments) != 1 || detail.Files[0].Attachments[0].ID != 2902300 {
		t.Fatalf("nested attachments were not decoded: %+v", detail)
	}
}

func TestAttachmentURLUsesBoundedOfficialLayout(t *testing.T) {
	initSettings(t)
	s := &Supplier{baseURL: "https://feed.animetosho.org", attachmentBaseURL: "https://animetosho.org"}
	detail := &detailResponse{Files: []detailFile{{Filename: "Show Name - 03.mkv"}}}
	attachment := attachmentInfo{ID: 2902300, Type: "subtitle"}
	attachment.Info.Codec, attachment.Info.Lang, attachment.Info.TrackNum = "ASS", "chi", 25
	got, err := s.attachmentURL(detail, feedItem{}, attachment)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "/storage/attach/002c491c/Show%20Name%20-%2003_track25.chi.ass.xz") {
		t.Fatalf("unexpected attachment URL: %s", got)
	}
}

func TestHTTPClientRejectsCredentialBearingEndpoint(t *testing.T) {
	s := &Supplier{}
	if _, err := s.httpClient(requestTimeout, "https://user:pass@feed.animetosho.org/json"); err == nil {
		t.Fatal("credential-bearing endpoint was accepted")
	}
}

func TestDecompressAttachmentRejectsHTML(t *testing.T) {
	var compressed bytes.Buffer
	writer, err := xz.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write([]byte("<html>blocked</html>"))
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = decompressAttachment(compressed.Bytes()); err == nil || !strings.Contains(err.Error(), "HTML") {
		t.Fatalf("expected HTML rejection, got %v", err)
	}
}

func TestLooksLikeSubtitleByCodec(t *testing.T) {
	if !looksLikeSubtitle([]byte("[Script Info]\nTitle: test"), "ASS") ||
		!looksLikeSubtitle([]byte("1\n00:00:01,000 --> 00:00:02,000\n你好"), "SRT") ||
		looksLikeSubtitle([]byte("rate limited"), "SRT") {
		t.Fatal("subtitle document validation failed")
	}
}
