package subtitle_best

import (
	"testing"

	"github.com/go-resty/resty/v2"
)

func TestConfigureHealthCheckClientBoundsProbe(t *testing.T) {
	client := resty.New().SetRetryCount(3)
	configured := configureHealthCheckClient(client)

	if configured != client {
		t.Fatal("health-check configuration should reuse the supplier client")
	}
	if got := client.GetClient().Timeout; got != subtitleBestHealthCheckTimeout {
		t.Fatalf("timeout = %v, want %v", got, subtitleBestHealthCheckTimeout)
	}
	if got := client.RetryCount; got != 0 {
		t.Fatalf("retry count = %d, want 0", got)
	}
}
