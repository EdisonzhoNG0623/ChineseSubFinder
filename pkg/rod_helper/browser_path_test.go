package rod_helper

import "testing"

func TestResolveLocalChromePath(t *testing.T) {
	t.Setenv("CSF_CHROME_BIN", "/opt/chrome/chrome")
	if got := resolveLocalChromePath(""); got != "/opt/chrome/chrome" {
		t.Fatalf("environment Chrome path = %q", got)
	}
	if got := resolveLocalChromePath("/custom/chrome"); got != "/custom/chrome" {
		t.Fatalf("configured Chrome path was not preserved: %q", got)
	}
}
