package release

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// releaseManifestFixture is deliberately unrelated to the build host; pulls
// authenticate it without authorising an installation on that host.
func releaseManifestFixture(t *testing.T) []byte {
	t.Helper()
	var manifest compatibility.Manifest
	data := []byte(`{"schema_version":1,"kind":"lexr.userspace-component-compatibility","component_id":"example-audio","release":"component-v1","capabilities":["audio"],"lexr":{"minimum_inclusive":"99.0.0","tested_through_inclusive":"99.0.0"},"targets":[{"architectures":["arm64"],"device_profiles":["surface-pro-11-x1e-oled"],"operating_systems":[{"id":"fedora","version_ranges":[{"minimum_inclusive":"44"}],"tested_versions":["44"]}],"kernels":[{"patch_line":"7.2.0","platform_flavour":"qcom-x1e","scope":"sp11","abi_generation":{"minimum_inclusive":19,"tested_through_inclusive":19}}]}]}`)
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	result, err := compatibility.Marshal(&manifest)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// TestPullAuthenticatesCompatibilityWithoutEvaluatingHost proves the independent
// pin is required even when all publisher-provided checksums agree.
func TestPullAuthenticatesCompatibilityWithoutEvaluatingHost(t *testing.T) {
	data := releaseManifestFixture(t)
	reference := compatibility.ReferenceFor(data)
	files := map[string][]byte{compatibility.Filename: data, "payload": []byte("payload")}
	files["SHA256SUMS"] = []byte(fmt.Sprintf("%s  %s\n%s  payload\n", digest(data), compatibility.Filename, digest(files["payload"])))
	server := releaseServer(t, "component-v1", files)
	defer server.Close()
	client := NewClient(server.Client())
	client.APIBaseURL = server.URL
	spec := Spec{Component: "example-audio", Repository: "owner/repo", Tag: "component-v1", ExactAssets: []string{"SHA256SUMS", compatibility.Filename, "payload"}, Compatibility: &reference}
	bundle, err := client.Download(context.Background(), spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Compatibility == nil || *bundle.Compatibility != reference {
		t.Fatal("manifest pin omitted from receipt")
	}
	reference.SHA256 = strings.Repeat("0", 64)
	if _, err := client.Download(context.Background(), spec, t.TempDir()); err == nil {
		t.Fatal("publisher checksums replaced independent pin")
	}
	spec.Compatibility = nil
	if _, err := client.Download(context.Background(), spec, t.TempDir()); err == nil {
		t.Fatal("manifest downloaded without independent pin")
	}
}
