package subhd

import (
	"context"
	"testing"
	"time"

	"github.com/go-rod/rod"
)

func TestApplyOperationTimeout(t *testing.T) {
	supplier := &Supplier{operationTimeout: 20 * time.Millisecond}
	browser := supplier.applyOperationTimeout(rod.New())
	defer browser.CancelTimeout()

	select {
	case <-browser.GetContext().Done():
		if browser.GetContext().Err() != context.DeadlineExceeded {
			t.Fatalf("browser context error = %v, want %v", browser.GetContext().Err(), context.DeadlineExceeded)
		}
	case <-time.After(time.Second):
		t.Fatal("browser operation context did not time out")
	}
}

func TestApplyOperationTimeoutUsesDefault(t *testing.T) {
	supplier := &Supplier{}
	browser := supplier.applyOperationTimeout(rod.New())
	defer browser.CancelTimeout()

	deadline, ok := browser.GetContext().Deadline()
	if !ok {
		t.Fatal("browser operation context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > subHDOperationTimeout {
		t.Fatalf("browser operation timeout = %v, want within (0, %v]", remaining, subHDOperationTimeout)
	}
}

func TestParsePrepareDownloadResponse(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantURL   string
		wantError bool
	}{
		{name: "success", body: `{"success":true,"url":"/down/JrRjGa"}`, wantURL: "/down/JrRjGa"},
		{name: "rejected", body: `{"success":false,"msg":"rate limited"}`, wantError: true},
		{name: "unsafe URL", body: `{"success":true,"url":"https://example.com/file.zip"}`, wantError: true},
		{name: "invalid JSON", body: `<html>blocked</html>`, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parsePrepareDownloadResponse(test.body)
			if test.wantError && err == nil {
				t.Fatalf("parsePrepareDownloadResponse(%q) unexpectedly succeeded", test.body)
			}
			if !test.wantError && err != nil {
				t.Fatalf("parsePrepareDownloadResponse(%q): %v", test.body, err)
			}
			if got != test.wantURL {
				t.Fatalf("parsePrepareDownloadResponse(%q) = %q, want %q", test.body, got, test.wantURL)
			}
		})
	}
}

func TestParseFinalDownloadResponse(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantURL   string
		wantError bool
	}{
		{name: "success", body: `{"success":true,"pass":true,"url":"https://dl.subhd.me/file.7z"}`, wantURL: "https://dl.subhd.me/file.7z"},
		{name: "not passed", body: `{"success":true,"pass":false,"msg":"verify first"}`, wantError: true},
		{name: "unsafe host suffix", body: `{"success":true,"pass":true,"url":"https://subhd.me.example.com/file.7z"}`, wantError: true},
		{name: "unsafe scheme", body: `{"success":true,"pass":true,"url":"http://dl.subhd.me/file.7z"}`, wantError: true},
		{name: "invalid JSON", body: `<html>blocked</html>`, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseFinalDownloadResponse(test.body)
			if test.wantError && err == nil {
				t.Fatalf("parseFinalDownloadResponse(%q) unexpectedly succeeded", test.body)
			}
			if !test.wantError && err != nil {
				t.Fatalf("parseFinalDownloadResponse(%q): %v", test.body, err)
			}
			if got != test.wantURL {
				t.Fatalf("parseFinalDownloadResponse(%q) = %q, want %q", test.body, got, test.wantURL)
			}
		})
	}
}
