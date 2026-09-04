package ubuntu

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// TestRemasterIntegration builds and validates a real image when all explicit
// integration paths are supplied; normal unit-test runs remain self-contained.
func TestRemasterIntegration(t *testing.T) {
	source := os.Getenv("LEXR_TEST_SOURCE_ISO")
	kernelDirectory := os.Getenv("LEXR_TEST_KERNEL_DIRECTORY")
	output := os.Getenv("LEXR_TEST_OUTPUT_ISO")
	if source == "" || kernelDirectory == "" || output == "" {
		t.Skip("set LEXR_TEST_SOURCE_ISO, LEXR_TEST_KERNEL_DIRECTORY, and LEXR_TEST_OUTPUT_ISO")
	}
	bundle, err := kernel.DiscoverLocalBundle(kernelDirectory)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	result, err := NewRemasterer(nil, os.Stdout).Create(ctx, Request{
		SourceISO:   source,
		OutputISO:   output,
		Bundle:      bundle,
		ToolVersion: "integration-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.OutputISO == "" || result.SHA256 == "" || result.Size == 0 {
		t.Fatalf("Create() returned incomplete result: %#v", result)
	}
}
