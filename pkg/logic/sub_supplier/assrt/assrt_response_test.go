package assrt

import (
	"encoding/json"
	"testing"
)

func TestSearchSubResultAcceptsRevisionStringOrNumber(t *testing.T) {
	for _, revision := range []string{`"12"`, `12`} {
		payload := `{"sub":{"subs":[{"id":1,"revision":` + revision + `}]},"status":0}`
		var result SearchSubResult
		if err := json.Unmarshal([]byte(payload), &result); err != nil {
			t.Fatalf("revision %s: %v", revision, err)
		}
		if len(result.Sub.Subs) != 1 {
			t.Fatalf("revision %s: got %d subtitles", revision, len(result.Sub.Subs))
		}
	}
}
