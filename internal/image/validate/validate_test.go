package validate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/fedora"
	"github.com/ooaklee/lexr.sh/internal/image/ubuntu"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// stubAdapter records router dispatch without invoking Docker or inspecting a
// real boot image.
type stubAdapter struct {
	report imagecontract.ValidationReport
	err    error
	calls  int
	path   string
}

// Validate implements the distribution adapter contract.
func (stub *stubAdapter) Validate(_ context.Context, path string) (imagecontract.ValidationReport, error) {
	stub.calls++
	stub.path = path
	return stub.report, stub.err
}

// TestValidatorDispatchesOnlyTheManifestAdapter proves the strict manifest
// decision reaches exactly one of the two compiled adapter factories.
func TestValidatorDispatchesOnlyTheManifestAdapter(t *testing.T) {
	t.Parallel()

	for _, adapterID := range []string{ubuntu.AdapterID, fedora.AdapterID} {
		adapterID := adapterID
		t.Run(adapterID, func(t *testing.T) {
			t.Parallel()
			absolute := filepath.Join(t.TempDir(), "generated.iso")
			routed := testRoutedImage(adapterID)
			selected := &stubAdapter{report: testAdapterReport(absolute, routed)}
			other := &stubAdapter{}
			cleanupCalls := 0
			routed.Cleanup = func() error {
				cleanupCalls++
				return nil
			}
			validator := &Validator{
				docker: &platform.Docker{},
				extract: func(context.Context, *platform.Docker, string) (routedImage, error) {
					return routed, nil
				},
				ubuntuFactory: func(*platform.Docker) adapterValidator {
					if adapterID == ubuntu.AdapterID {
						return selected
					}
					return other
				},
				fedoraFactory: func(*platform.Docker) adapterValidator {
					if adapterID == fedora.AdapterID {
						return selected
					}
					return other
				},
			}

			report, err := validator.Validate(context.Background(), absolute)
			if err != nil {
				t.Fatal(err)
			}
			if !report.Valid || selected.calls != 1 || other.calls != 0 {
				t.Fatalf("report/dispatch = %#v, selected=%d other=%d", report, selected.calls, other.calls)
			}
			if selected.path != absolute || cleanupCalls != 1 {
				t.Fatalf("selected path = %q, cleanup calls = %d", selected.path, cleanupCalls)
			}
		})
	}
}

// TestValidatorRejectsUntrustedRoutingValues verifies neither unknown adapter
// names nor unsupported schema versions can reach an adapter factory.
func TestValidatorRejectsUntrustedRoutingValues(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		adapter  string
		schema   int
		wantText string
	}{
		{name: "unknown adapter", adapter: "arch-live", schema: imagecontract.ManifestSchemaVersion, wantText: "unsupported generated-image adapter"},
		{name: "mis-cased allowlisted adapter", adapter: "Ubuntu-Casper", schema: imagecontract.ManifestSchemaVersion, wantText: "unsupported generated-image adapter"},
		{name: "future schema", adapter: "ubuntu-casper", schema: imagecontract.ManifestSchemaVersion + 1, wantText: "unsupported image manifest schema"},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			routed := testRoutedImage(testCase.adapter)
			routed.Manifest.SchemaVersion = testCase.schema
			factoryCalls := 0
			factory := func(*platform.Docker) adapterValidator {
				factoryCalls++
				return &stubAdapter{}
			}
			validator := &Validator{
				docker: &platform.Docker{},
				extract: func(context.Context, *platform.Docker, string) (routedImage, error) {
					return routed, nil
				},
				ubuntuFactory: factory,
				fedoraFactory: factory,
			}
			_, err := validator.Validate(context.Background(), filepath.Join(t.TempDir(), "image.iso"))
			if err == nil || !strings.Contains(err.Error(), testCase.wantText) {
				t.Fatalf("Validate() error = %v, want %q", err, testCase.wantText)
			}
			if factoryCalls != 0 {
				t.Fatalf("adapter factory called %d times for rejected manifest", factoryCalls)
			}
		})
	}
}

// TestValidatorBindsAdapterReportToRoutingManifest rejects a validator that
// reports success for another path, adapter, or embedded manifest identity.
func TestValidatorBindsAdapterReportToRoutingManifest(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		mutate func(*imagecontract.ValidationReport)
		text   string
	}{
		{name: "different path", mutate: func(report *imagecontract.ValidationReport) { report.Path += ".other" }, text: "different image path"},
		{name: "invalid success", mutate: func(report *imagecontract.ValidationReport) { report.Valid = false }, text: "success with an invalid report"},
		{name: "different adapter", mutate: func(report *imagecontract.ValidationReport) { report.Adapter = fedora.AdapterID }, text: "routed manifest identifies"},
		{name: "different manifest digest", mutate: func(report *imagecontract.ValidationReport) { report.ManifestSHA256 = strings.Repeat("b", 64) }, text: "embedded manifest changed"},
		{name: "different manifest size", mutate: func(report *imagecontract.ValidationReport) { report.ManifestSize++ }, text: "embedded manifest changed"},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			absolute := filepath.Join(t.TempDir(), "generated.iso")
			routed := testRoutedImage("ubuntu-casper")
			report := testAdapterReport(absolute, routed)
			testCase.mutate(&report)
			adapter := &stubAdapter{report: report}
			validator := &Validator{
				docker: &platform.Docker{},
				extract: func(context.Context, *platform.Docker, string) (routedImage, error) {
					return routed, nil
				},
				ubuntuFactory: func(*platform.Docker) adapterValidator { return adapter },
			}
			_, err := validator.Validate(context.Background(), absolute)
			if err == nil || !strings.Contains(err.Error(), testCase.text) {
				t.Fatalf("Validate() error = %v, want %q", err, testCase.text)
			}
		})
	}
}

// extractionRunner simulates only the fixed Docker operations needed by the
// production manifest extractor and writes controlled bytes into its workspace.
type extractionRunner struct {
	manifest      []byte
	extractScript string
	extractArgs   []string
}

// Capture satisfies platform.Runner for Docker availability and tools-image
// inspection.
func (runner *extractionRunner) Capture(_ context.Context, command platform.Command) ([]byte, error) {
	if command.Name != "docker" || len(command.Args) == 0 {
		return nil, errors.New("unexpected captured command")
	}
	switch command.Args[0] {
	case "info":
		return []byte("test-server"), nil
	case "image":
		return []byte("test-tools-image"), nil
	default:
		return nil, errors.New("unexpected Docker capture")
	}
}

// Run simulates the bounded xorriso container by writing the configured
// manifest beneath the mounted private workspace.
func (runner *extractionRunner) Run(_ context.Context, command platform.Command) error {
	if command.Name != "docker" || len(command.Args) == 0 || command.Args[0] != "run" {
		return errors.New("unexpected streamed command")
	}
	runner.extractArgs = append([]string(nil), command.Args...)
	workspace := ""
	probe := ""
	for index, argument := range command.Args {
		if argument == "--volume" && index+1 < len(command.Args) &&
			strings.HasSuffix(command.Args[index+1], ":/work") {
			workspace = strings.TrimSuffix(command.Args[index+1], ":/work")
		}
		if strings.Contains(argument, "xorriso -osirrox") {
			runner.extractScript = argument
		}
		if strings.HasPrefix(argument, ".lexr-workspace-owner-") {
			probe = argument
		}
	}
	if workspace == "" {
		return errors.New("Docker command has no workspace")
	}
	if runtime.GOOS == "darwin" && probe != "" {
		if err := os.WriteFile(filepath.Join(workspace, probe), []byte("lexr-workspace-owner"), 0o600); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(workspace, "manifest.json"), runner.manifest, 0o644)
}

// TestExtractRoutingManifestStrictlyDecodesBoundedBytes exercises the real
// filesystem and Docker-command boundary without requiring a Docker daemon.
func TestExtractRoutingManifestStrictlyDecodesBoundedBytes(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		manifest func(*testing.T) []byte
		wantText string
	}{
		{name: "valid", manifest: func(t *testing.T) []byte { return testManifestJSON(t, fedora.AdapterID) }},
		{name: "unknown JSON field", manifest: func(t *testing.T) []byte {
			valid := testManifestJSON(t, fedora.AdapterID)
			return []byte(strings.Replace(string(valid), `{`, `{"unexpected":true,`, 1))
		}, wantText: "unknown or mis-cased field"},
		{name: "oversized", manifest: func(*testing.T) []byte {
			return []byte(strings.Repeat(" ", imagecontract.MaximumManifestSize+1))
		}, wantText: "not a bounded"},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			isoPath := filepath.Join(t.TempDir(), "generated.iso")
			if err := os.WriteFile(isoPath, []byte("bounded ISO fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			runner := &extractionRunner{manifest: testCase.manifest(t)}
			routed, err := extractRoutingManifest(context.Background(), platform.NewDocker(runner), isoPath)
			if testCase.wantText != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantText) {
					t.Fatalf("extractRoutingManifest() error = %v, want %q", err, testCase.wantText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := routed.Cleanup(); err != nil {
					t.Error(err)
				}
			}()
			if routed.Manifest.Adapter != fedora.AdapterID || routed.ManifestSize != int64(len(runner.manifest)) {
				t.Fatalf("routed manifest = %#v", routed)
			}
			if !strings.Contains(runner.extractScript, "ulimit -f") ||
				!strings.Contains(runner.extractScript, "/sp11/lexr-manifest.json") {
				t.Fatalf("unbounded or wrong extraction script: %q", runner.extractScript)
			}
			if strings.Contains(runner.extractScript, "chmod") {
				t.Fatalf("routing extraction rewrites package modes: %q", runner.extractScript)
			}
			for _, required := range [][2]string{
				{"--network", "none"},
				{"--cap-drop", "ALL"},
				{"--security-opt", "no-new-privileges"},
				{"--volume", isoPath + ":/input.iso:ro"},
			} {
				if !hasAdjacentArguments(runner.extractArgs, required[0], required[1]) {
					t.Errorf("Docker extraction arguments do not contain %q %q: %q", required[0], required[1], runner.extractArgs)
				}
			}
			if !hasArgument(runner.extractArgs, "--read-only") {
				t.Errorf("Docker extraction arguments do not make the container read-only: %q", runner.extractArgs)
			}
		})
	}
}

// hasArgument reports whether one exact command argument is present.
func hasArgument(arguments []string, want string) bool {
	for _, argument := range arguments {
		if argument == want {
			return true
		}
	}
	return false
}

// hasAdjacentArguments reports whether an exact option and value pair is
// present in a command argument list.
func hasAdjacentArguments(arguments []string, first string, second string) bool {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == first && arguments[index+1] == second {
			return true
		}
	}
	return false
}

// testRoutedImage returns a decoded routing fixture with stable manifest
// identity values used by adapter-dispatch tests.
func testRoutedImage(adapterID string) routedImage {
	return routedImage{
		Manifest: imagecontract.Manifest{
			SchemaVersion: imagecontract.ManifestSchemaVersion,
			Layout:        "hybrid-iso",
			Adapter:       adapterID,
		},
		ManifestSHA256: strings.Repeat("a", 64),
		ManifestSize:   123,
	}
}

// testAdapterReport returns a successful report bound to one routing fixture.
func testAdapterReport(path string, routed routedImage) imagecontract.ValidationReport {
	return imagecontract.ValidationReport{
		Valid:          true,
		Path:           path,
		Adapter:        routed.Manifest.Adapter,
		ManifestSHA256: routed.ManifestSHA256,
		ManifestSize:   routed.ManifestSize,
	}
}

// testManifestJSON encodes every required collection as an array so the shared
// strict manifest decoder receives a shape-complete fixture.
func testManifestJSON(t *testing.T, adapterID string) []byte {
	t.Helper()
	manifest := imagecontract.Manifest{
		SchemaVersion: imagecontract.ManifestSchemaVersion,
		Layout:        "hybrid-iso",
		Adapter:       adapterID,
		KernelBundle: kernel.Bundle{
			Packages:    []kernel.Package{},
			DeviceTrees: []kernel.DeviceTree{},
		},
		BootArtifacts:  imagecontract.BootArtifactRecord{DTBs: []imagecontract.ArtifactRecord{}},
		MediaDiscovery: imagecontract.MediaDiscoveryRecord{Evidence: []imagecontract.MediaDiscoveryEvidence{}},
		CompanionBundle: imagecontract.CompanionBundleRecord{
			Included:  false,
			Root:      "sp11/companion",
			Reason:    "not-requested",
			Userspace: []imagecontract.OfflineUserspaceRecord{},
		},
		BootArguments: []string{},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
