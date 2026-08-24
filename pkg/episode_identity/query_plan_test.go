package episode_identity

import "testing"

func TestBuildSearchPlanIncludesFairyTailAbsoluteFallback(t *testing.T) {
	plan := BuildSearchPlan([]string{"妖精的尾巴", "Fairy Tail", "Fairy Tail"}, Identity{
		Season: 8, Episode: 11, AbsoluteEpisode: 288,
	})

	want := map[string]bool{
		"妖精的尾巴 S08E11":      false,
		"妖精的尾巴 E288":        false,
		"妖精的尾巴 EP288":       false,
		"妖精的尾巴 #288":        false,
		"妖精的尾巴 288":         false,
		"Fairy Tail S08E11": false,
		"Fairy Tail E288":   false,
	}
	for _, query := range plan {
		if _, exists := want[query.Query]; exists {
			want[query.Query] = true
		}
	}
	for query, found := range want {
		if !found {
			t.Fatalf("missing query %q in %#v", query, plan)
		}
	}
}

func TestBuildSearchPlanDeduplicatesAndOrdersRisk(t *testing.T) {
	plan := BuildSearchPlan([]string{"Show", " show "}, Identity{
		Season: 2, Episode: 3, AbsoluteEpisode: 15,
	})
	if len(plan) != 5 {
		t.Fatalf("plan length = %d, want 5: %#v", len(plan), plan)
	}
	if plan[0].Kind != QueryAired || plan[len(plan)-1].Query != "Show 15" {
		t.Fatalf("unexpected query order: %#v", plan)
	}
}
