package builderimage

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestDependencyImageOptIn acquires signed distribution packages in a local
// owned Docker container only when explicitly selected. It needs a native
// Linux ARM64 engine and at least 6 GiB of container-root free space. It retains
// the image/container and complete diagnostics; it deletes no caches, mounts
// no host source or devices, and does not run sensor code or contact hardware.
func TestDependencyImageOptIn(t *testing.T) {
	if os.Getenv("LEXR_TEST_SENSOR_DEPENDENCY_IMAGE") != "1" {
		t.Skip("set LEXR_TEST_SENSOR_DEPENDENCY_IMAGE=1 to run the networked dependency-only producer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Minute)
	defer cancel()
	manager := New(platform.NewDiagnosticRunner(nil))
	// Journal the freshly generated owned name before creation, so a terminated
	// test client still leaves an exact observation target in retained output.
	originalToken := manager.token
	manager.token = func() (string, error) {
		token, err := originalToken()
		if err == nil {
			t.Logf("dependency-producer-owned-name=%s", containerPrefix+token)
		}
		return token, err
	}
	result, err := manager.Prepare(ctx, Request{})
	data, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	t.Logf("dependency-producer-result=%s", data)
	if err != nil {
		t.Fatal(err)
	}
	if result.Prepared == nil || result.Provenance == nil || result.TerminalUnknown || result.Terminal == nil || !successful(*result.Terminal) {
		t.Fatal("producer did not return verified terminal/image authority")
	}
	if result.Prepared.ImageID() != result.ObservedImageID || result.Prepared.Provenance() != *result.Provenance {
		t.Fatal("opaque result differs from the observed receipt")
	}
	reuse, err := manager.Prepare(ctx, Request{NoPull: true, Prepared: result.Prepared})
	if err != nil {
		t.Fatal(err)
	}
	if reuse.Prepared != result.Prepared || reuse.Provenance == nil || *reuse.Provenance != *result.Provenance {
		t.Fatal("same-process NoPull reused different authority")
	}
	t.Logf("dependency-producer-no-pull-reuse=%s", reuse.Prepared.ImageID())
}
