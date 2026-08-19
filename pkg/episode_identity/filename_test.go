package episode_identity

import "testing"

func TestFilenameContainsAbsoluteEpisode(t *testing.T) {
	for _, name := range []string{"Fairy.Tail.288.ass", "妖精的尾巴 EP288 简体.srt", "[288] Fairy Tail.ass"} {
		if !FilenameContainsAbsoluteEpisode(name, 288) {
			t.Fatalf("absolute episode not detected in %q", name)
		}
	}
	for _, name := range []string{"Fairy.Tail.2026.1080p.x264.ass", "Fairy.Tail.289.ass", "show288.ass"} {
		if FilenameContainsAbsoluteEpisode(name, 288) {
			t.Fatalf("false absolute episode match in %q", name)
		}
	}
}
