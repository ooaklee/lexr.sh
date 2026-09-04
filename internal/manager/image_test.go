package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	lexr "github.com/ooaklee/lexr.sh"
	"github.com/ooaklee/lexr.sh/internal/catalog"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/kernel/release"
	"github.com/ooaklee/lexr.sh/internal/platform"
	userspacecatalog "github.com/ooaklee/lexr.sh/internal/userspace/catalog"
	userspacemanager "github.com/ooaklee/lexr.sh/internal/userspace/manager"
)

// companionProbeRunner records host-toolchain probes without starting external
// processes during image-manager tests.
type companionProbeRunner struct {
	commands  []platform.Command
	output    []byte
	err       error
	responses map[string][]byte
}

// Run rejects unexpected streaming commands because companion preflight needs
// only captured Go version output.
func (*companionProbeRunner) Run(context.Context, platform.Command) error {
	return errors.New("unexpected Run call")
}

// Capture records one probe and returns the configured deterministic result.
func (r *companionProbeRunner) Capture(_ context.Context, command platform.Command) ([]byte, error) {
	r.commands = append(r.commands, command)
	if r.err != nil {
		return nil, r.err
	}
	if response, ok := r.responses[probeCommandKey(command)]; ok {
		return append([]byte(nil), response...), nil
	}
	if r.output == nil {
		if command.Name == "go" && reflect.DeepEqual(command.Args, []string{"version"}) {
			return []byte("go version go1.25.0 test/arm64\n"), nil
		}
		if command.Name == "git" && slices.Contains(command.Args, "rev-parse") {
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		}
		if command.Name == "git" && slices.Contains(command.Args, "show") {
			return []byte("2026-08-30T12:00:00Z\n"), nil
		}
		if command.Name == "git" && slices.Contains(command.Args, "status") {
			return []byte{}, nil
		}
		return nil, fmt.Errorf("unexpected capture command: %s", probeCommandKey(command))
	}
	return append([]byte(nil), r.output...), nil
}

// probeCommandKey creates a stable test lookup key without shell rendering.
func probeCommandKey(command platform.Command) string {
	return command.Name + "\x00" + strings.Join(command.Args, "\x00")
}

// newCompanionTestManager creates the catalogue and command boundaries needed
// to resolve a core-only companion without network access.
func newCompanionTestManager(runner *companionProbeRunner) *ImageManager {
	return &ImageManager{
		CompanionRunner: runner,
		Userspace: userspacemanager.New(
			userspacecatalog.NewLoader(lexr.UserspaceCatalogFS(), "supported-userspace.json"), nil, nil,
		),
	}
}

// newImagePlanTestManager supplies the shipped catalogue and production adapter
// wiring needed for side-effect-free image planning tests.
func newImagePlanTestManager() *ImageManager {
	return NewImageManager(catalog.NewLoader(lexr.CatalogFS(), "supported-isos.json"), io.Discard)
}

// TestImageManagerPlanDefaultsAndDeterminism verifies default source and kernel
// inputs produce the same ordered, serialisable execution plan on every call.
func TestImageManagerPlanDefaultsAndDeterminism(t *testing.T) {
	t.Parallel()

	manager := newImagePlanTestManager()
	request := CreateImageRequest{Output: "/output/lexr.iso"}
	first, err := manager.Plan(request)
	if err != nil {
		t.Fatalf("first Plan() error = %v", err)
	}
	second, err := manager.Plan(request)
	if err != nil {
		t.Fatalf("second Plan() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Plan() is not deterministic:\nfirst: %#v\nsecond: %#v", first, second)
	}

	var firstJSON bytes.Buffer
	if err := first.WriteJSON(&firstJSON); err != nil {
		t.Fatalf("first WriteJSON() error = %v", err)
	}
	var secondJSON bytes.Buffer
	if err := second.WriteJSON(&secondJSON); err != nil {
		t.Fatalf("second WriteJSON() error = %v", err)
	}
	if firstJSON.String() != secondJSON.String() {
		t.Fatalf("serialized plans differ:\nfirst:\n%s\nsecond:\n%s", firstJSON.String(), secondJSON.String())
	}

	wantStepIDs := []string{
		"verify-source", "verify-kernel", "stage-companion", "prepare-tools", "extract-live-root", "install-kernel",
		"assemble-initramfs-root", "build-initramfs", "bind-live-media", "pair-device-trees", "repack-live-root",
		"replay-hybrid-boot", "validate-output", "publish-output",
	}
	gotStepIDs := make([]string, 0, len(first.Steps))
	for _, step := range first.Steps {
		gotStepIDs = append(gotStepIDs, step.ID)
	}
	if !reflect.DeepEqual(gotStepIDs, wantStepIDs) {
		t.Fatalf("Plan() step IDs = %v, want %v", gotStepIDs, wantStepIDs)
	}
	if want := "catalog:" + DefaultCatalogID; first.Steps[0].Inputs["path"] != want {
		t.Errorf("source input = %q, want %q", first.Steps[0].Inputs["path"], want)
	}
	if want := release.DefaultRepository + "@latest"; first.Steps[1].Inputs["release"] != want {
		t.Errorf("kernel input = %q, want %q", first.Steps[1].Inputs["release"], want)
	}
	if first.Steps[2].Inputs["source"] != "not-requested" {
		t.Errorf("companion source = %q, want explicit omission", first.Steps[2].Inputs["source"])
	}
	if first.Steps[2].Inputs["userspace"] != "none" {
		t.Errorf("companion userspace = %q, want explicit omission", first.Steps[2].Inputs["userspace"])
	}
	if first.Steps[len(first.Steps)-1].Inputs["path"] != request.Output {
		t.Errorf("publish path = %q, want %q", first.Steps[len(first.Steps)-1].Inputs["path"], request.Output)
	}
}

// TestImageManagerPlanUsesExplicitLocalInputs verifies user-provided source,
// kernel directory, and output replace their plan defaults after catalogue selection.
func TestImageManagerPlanUsesExplicitLocalInputs(t *testing.T) {
	t.Parallel()

	operationPlan, err := newImagePlanTestManager().Plan(CreateImageRequest{
		CatalogID:                DefaultCatalogID,
		Source:                   "/inputs/source.iso",
		KernelDirectory:          "/inputs/kernel",
		KernelProfile:            "surface-pro-11-x1e-oled",
		CompanionSourceDirectory: "/inputs/lexr",
		CompanionUserspace:       []string{"iptsd"},
		Output:                   "/output/result.iso",
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if operationPlan.Steps[0].Inputs["path"] != "/inputs/source.iso" ||
		operationPlan.Steps[1].Inputs["release"] != "/inputs/kernel" ||
		operationPlan.Steps[1].Inputs["profile"] != "surface-pro-11-x1e-oled" ||
		operationPlan.Steps[2].Inputs["source"] != "/inputs/lexr" ||
		operationPlan.Steps[2].Inputs["userspace"] != companion.IPTSDOfflineComponentID ||
		operationPlan.Steps[len(operationPlan.Steps)-1].Inputs["path"] != "/output/result.iso" {
		t.Fatalf("Plan() explicit inputs = %#v", operationPlan.Steps)
	}
}

// TestProjectKernelBundleForImageRequiresExplicitExternalProfile proves image
// creation records one reviewed platform without mutating the source bundle.
func TestProjectKernelBundleForImageRequiresExplicitExternalProfile(t *testing.T) {
	const (
		abi     = "7.2.2-jg-0sp11v10-qcom-x1e"
		version = "7.2.2-jg-0sp11v10"
	)
	packages := []kernel.Package{
		{Role: kernel.RoleImage, Name: "linux-image-" + abi + "_" + version + "_arm64.deb", SHA256: strings.Repeat("1", 64), Size: 1},
		{Role: kernel.RoleModules, Name: "linux-modules-" + abi + "_" + version + "_arm64.deb", SHA256: strings.Repeat("2", 64), Size: 2},
		{Role: kernel.RoleBootSupport, Name: "lexr-kernel-boot-support_" + version + "_all.deb", SHA256: strings.Repeat("3", 64), Size: 3},
	}
	trees := []kernel.DeviceTree{
		{
			Device: "surface-pro-11-x1e-oled", Basename: "x1e80100-microsoft-denali-oled.dtb",
			Path:              "usr/lib/firmware/" + abi + "/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb",
			CompatibleStrings: []string{"microsoft,denali-oled", "microsoft,denali"}, SHA256: strings.Repeat("a", 64),
			Selectors: []kernel.DeviceTreeSelector{{Kind: kernel.DeviceTreeSelectorCompatible, Value: "microsoft,denali-oled"}}, Required: true,
		},
		{
			Device: "surface-pro-11-x1p-lcd", Basename: "x1p64100-microsoft-denali.dtb",
			Path:              "usr/lib/firmware/" + abi + "/device-tree/qcom/x1p64100-microsoft-denali.dtb",
			CompatibleStrings: []string{"microsoft,denali-lcd", "microsoft,denali"}, SHA256: strings.Repeat("b", 64),
			Selectors: []kernel.DeviceTreeSelector{{Kind: kernel.DeviceTreeSelectorCompatible, Value: "microsoft,denali-lcd"}}, Required: true,
		},
	}
	bundle, err := kernel.NewBundle(kernel.BundleOptions{
		Release: "fixture", RequestedBootImageMode: kernel.RequestedBootImageModeSource,
		EffectiveDTBDelivery: kernel.DTBDeliveryExternalRequired, Packages: packages, DeviceTrees: trees,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projectKernelBundleForImage(bundle, ""); err == nil || !strings.Contains(err.Error(), "requires --kernel-profile") {
		t.Fatalf("missing external profile error = %v", err)
	}
	if _, err := projectKernelBundleForImage(bundle, "unknown"); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("unknown external profile error = %v", err)
	}
	if _, err := projectKernelBundleForImage(bundle, " surface-pro-11-x1e-oled"); err == nil || !strings.Contains(err.Error(), "without surrounding whitespace") {
		t.Fatalf("padded external profile error = %v", err)
	}
	optional := bundle
	optional.DeviceTrees = kernel.CloneDeviceTrees(bundle.DeviceTrees)
	optional.DeviceTrees[0].Required = false
	if _, err := projectKernelBundleForImage(optional, optional.DeviceTrees[0].Device); err == nil || !strings.Contains(err.Error(), "not declared as supported") {
		t.Fatalf("optional external profile error = %v", err)
	}
	projected, err := projectKernelBundleForImage(bundle, "surface-pro-11-x1e-oled")
	if err != nil {
		t.Fatal(err)
	}
	required := make([]string, 0, 1)
	for _, tree := range projected.DeviceTrees {
		if tree.Required {
			required = append(required, tree.Device)
		}
	}
	if !slices.Equal(required, []string{"surface-pro-11-x1e-oled"}) {
		t.Fatalf("projected required profiles = %v", required)
	}
	if !bundle.DeviceTrees[0].Required || !bundle.DeviceTrees[1].Required {
		t.Fatal("profile projection mutated the authoritative source bundle")
	}
	if _, err := projectKernelBundleForImage(kernel.Bundle{EffectiveDTBDelivery: kernel.DTBDeliveryEmbedded}, "surface-pro-11-x1e-oled"); err == nil || !strings.Contains(err.Error(), "Stubble selects") {
		t.Fatalf("embedded profile error = %v", err)
	}
}

// TestImageManagerPlanValidatesCatalogueSelection verifies dry runs reject the
// same missing, unsupported, and invalid catalogue inputs as real creation.
func TestImageManagerPlanValidatesCatalogueSelection(t *testing.T) {
	t.Parallel()

	t.Run("missing entry", func(t *testing.T) {
		t.Parallel()

		_, err := newImagePlanTestManager().Plan(CreateImageRequest{CatalogID: "missing-image", Output: "/output/result.iso"})
		if err == nil || !strings.Contains(err.Error(), `catalog entry "missing-image" was not found`) {
			t.Fatalf("Plan(missing entry) error = %v", err)
		}
	})

	t.Run("catalogue-only entry", func(t *testing.T) {
		t.Parallel()

		_, err := newImagePlanTestManager().Plan(CreateImageRequest{CatalogID: "debian-13-6-0-dvd-1", Output: "/output/result.iso"})
		if err == nil || !strings.Contains(err.Error(), "catalog-only and cannot yet be created") {
			t.Fatalf("Plan(catalogue-only entry) error = %v", err)
		}
	})

	t.Run("invalid override", func(t *testing.T) {
		t.Parallel()

		overridePath := filepath.Join(t.TempDir(), "invalid-catalog.json")
		if err := os.WriteFile(overridePath, []byte(`{"schema_version":99,"description":"","entries":[]}`), 0o600); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
		_, err := newImagePlanTestManager().Plan(CreateImageRequest{CatalogPath: overridePath, Output: "/output/result.iso"})
		if err == nil || !strings.Contains(err.Error(), "schema_version") {
			t.Fatalf("Plan(invalid override) error = %v", err)
		}
	})
}

// TestEffectiveSourceSHA256 verifies image planning and execution share caller
// precedence, publisher fallback, normalisation, and the unpinned case.
func TestEffectiveSourceSHA256(t *testing.T) {
	t.Parallel()

	publisherDigest := strings.Repeat("a", 64)
	callerDigest := strings.Repeat("B", 64)
	tests := []struct {
		name     string
		request  CreateImageRequest
		checksum *catalog.Checksum
		want     string
	}{
		{name: "caller overrides publisher", request: CreateImageRequest{SourceSHA256: "  " + callerDigest + "  "}, checksum: &catalog.Checksum{Algorithm: "sha256", Value: publisherDigest}, want: strings.ToLower(callerDigest)},
		{name: "publisher fallback", checksum: &catalog.Checksum{Algorithm: "sha256", Value: publisherDigest}, want: publisherDigest},
		{name: "incompatible publisher digest", checksum: &catalog.Checksum{Algorithm: "sha512", Value: strings.Repeat("c", 128)}},
		{name: "unpinned"},
	}
	for _, test := range tests {
		if got := effectiveSourceSHA256(test.request, catalog.Entry{Checksum: test.checksum}); got != test.want {
			t.Errorf("effectiveSourceSHA256(%s) = %q, want %q", test.name, got, test.want)
		}
	}
}

// TestImageManagerPlanRejectsCompanionUserspaceWithoutSource verifies dry runs
// enforce the same required source relationship as real image creation.
func TestImageManagerPlanRejectsCompanionUserspaceWithoutSource(t *testing.T) {
	t.Parallel()

	_, err := (&ImageManager{}).Plan(CreateImageRequest{
		CompanionUserspace: []string{"iptsd"},
		Output:             "/output/result.iso",
	})
	if err == nil || !strings.Contains(err.Error(), "--companion-source-dir") {
		t.Fatalf("Plan(companion userspace without source) error = %v", err)
	}
}

// TestImageManagerPlanRequiresOutput verifies image planning rejects blank output
// paths before constructing any execution steps.
func TestImageManagerPlanRequiresOutput(t *testing.T) {
	t.Parallel()

	for _, output := range []string{"", " \t\n"} {
		_, err := (&ImageManager{}).Plan(CreateImageRequest{Output: output})
		if err == nil || !strings.Contains(err.Error(), "output ISO path is required") {
			t.Errorf("Plan(Output=%q) error = %v", output, err)
		}
	}
}

// TestImageManagerPlanRejectsNonPortableISOOutput verifies manager planning
// refuses output names that cannot enter the image-release contract.
func TestImageManagerPlanRejectsNonPortableISOOutput(t *testing.T) {
	t.Parallel()

	for _, output := range []string{
		"/output/result.img",
		"/output/not portable.iso",
		"/output/.hidden.iso",
		"/output/result.iso.tmp",
		"/output/" + strings.Repeat("a", 197) + ".iso",
	} {
		_, err := newImagePlanTestManager().Plan(CreateImageRequest{Output: output})
		if err == nil || !strings.Contains(err.Error(), "portable .iso filename") {
			t.Errorf("Plan(Output=%q) error = %v", output, err)
		}
	}
}

// TestImageManagerCreateRejectsCatalogOnlyEntryBeforeExecution verifies an image
// listed for discovery alone cannot reach download or remaster execution.
func TestImageManagerCreateRejectsCatalogOnlyEntryBeforeExecution(t *testing.T) {
	t.Parallel()

	loader := catalog.NewLoader(lexr.CatalogFS(), "supported-isos.json")
	manager := NewImageManager(loader, io.Discard)
	_, err := manager.Create(context.Background(), CreateImageRequest{
		CatalogID: "debian-13-6-0-dvd-1",
		Output:    filepath.Join(t.TempDir(), "output.iso"),
	})
	if err == nil || !strings.Contains(err.Error(), "catalog-only and cannot yet be created") {
		t.Fatalf("Create(catalog-only) error = %v", err)
	}
}

// TestImageManagerCreateRejectsUnknownCatalogEntryBeforeExecution verifies an
// unknown catalogue selector fails before any external image-building work begins.
func TestImageManagerCreateRejectsUnknownCatalogEntryBeforeExecution(t *testing.T) {
	t.Parallel()

	loader := catalog.NewLoader(lexr.CatalogFS(), "supported-isos.json")
	manager := NewImageManager(loader, io.Discard)
	_, err := manager.Create(context.Background(), CreateImageRequest{
		CatalogID: "missing-image",
		Output:    filepath.Join(t.TempDir(), "output.iso"),
	})
	if err == nil || !strings.Contains(err.Error(), `catalog entry "missing-image" was not found`) {
		t.Fatalf("Create(missing catalog entry) error = %v", err)
	}
}

// TestImageManagerCreateChecksDependenciesBeforeExternalWork verifies an
// incompletely wired manager reports its missing dependencies without side effects.
func TestImageManagerCreateChecksDependenciesBeforeExternalWork(t *testing.T) {
	t.Parallel()

	_, err := (&ImageManager{}).Create(context.Background(), CreateImageRequest{Output: "output.iso"})
	if err == nil || !strings.Contains(err.Error(), "dependencies are incomplete") {
		t.Fatalf("Create(incomplete manager) error = %v", err)
	}
}

// TestImageManagerResolveLocalSource verifies a local ISO is accepted with a
// case-insensitive matching digest and rejected when its checksum differs.
func TestImageManagerResolveLocalSource(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	source := filepath.Join(directory, "source.iso")
	content := []byte("local source image")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile(source) error = %v", err)
	}
	digestBytes := sha256.Sum256(content)
	digest := hex.EncodeToString(digestBytes[:])

	manager := &ImageManager{}
	path, gotDigest, err := manager.resolveSource(context.Background(), CreateImageRequest{
		Source:       source,
		SourceSHA256: strings.ToUpper(digest),
	}, catalog.Entry{ID: "local-source"}, directory)
	if err != nil {
		t.Fatalf("resolveSource() error = %v", err)
	}
	if path != source || gotDigest != digest {
		t.Fatalf("resolveSource() path/digest = %q/%q, want %q/%q", path, gotDigest, source, digest)
	}

	_, _, err = manager.resolveSource(context.Background(), CreateImageRequest{
		Source:       source,
		SourceSHA256: strings.Repeat("0", 64),
	}, catalog.Entry{ID: "local-source"}, directory)
	if err == nil || !strings.Contains(err.Error(), "source ISO SHA-256 mismatch") {
		t.Fatalf("resolveSource(checksum mismatch) error = %v", err)
	}
}

// TestImageManagerResolveLocalKernelBundle verifies the image manager discovers
// and hashes a complete Surface runtime package pair from an explicit directory.
func TestImageManagerResolveLocalKernelBundle(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	abi := "7.2.0-jg-0sp11v19-qcom-x1e"
	version := "7.2.0-jg-0sp11v19"
	packageNames := []string{
		"linux-image-" + abi + "_" + version + "_arm64.deb",
		"linux-modules-" + abi + "_" + version + "_arm64.deb",
	}
	packages := make([]kernel.Package, 0, len(packageNames))
	for _, name := range packageNames {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%s) error = %v", name, err)
		}
		digest := sha256.Sum256([]byte(name))
		role, _, _, err := kernel.ParsePackageName(name)
		if err != nil {
			t.Fatal(err)
		}
		packages = append(packages, kernel.Package{
			Role: role, Name: name, Path: filepath.Join(directory, name), SHA256: hex.EncodeToString(digest[:]), Size: int64(len(name)),
		})
	}
	manifest, err := kernel.NewBundle(kernel.BundleOptions{
		Release: "fixture", RequestedBootImageMode: kernel.RequestedBootImageModeStubble,
		EffectiveDTBDelivery: kernel.DTBDeliveryEmbedded, EmbeddedDTBCount: 2,
		DTBSelectionProvenance: &kernel.DTBSelectionProvenance{
			Tool: "stubble", Version: "fixture-1", DatabaseSHA256: strings.Repeat("d", 64), StubSHA256: strings.Repeat("1", 64), HelperSHA256: strings.Repeat("2", 64), SBATSHA256: strings.Repeat("3", 64),
			UKifyTool: "ukify", UKifyPackage: "systemd-ukify", UKifyVersion: "258.1-1", UKifySHA256: strings.Repeat("4", 64),
			Selections: []kernel.DeviceTreeSelectionEvidence{
				{Device: "surface-pro-11-x1e-oled", Records: []kernel.DTBSelectionRecord{{Source: "hwids", Compatible: "microsoft,denali", HWIDs: []string{"11111111-1111-5111-8111-111111111111"}}}},
				{Device: "surface-pro-11-x1p-lcd", Records: []kernel.DTBSelectionRecord{{Source: "hwids", Compatible: "microsoft,denali-x1p", HWIDs: []string{"22222222-2222-5222-8222-222222222222"}}}},
			},
		},
		Packages: packages,
		DeviceTrees: []kernel.DeviceTree{
			{Device: "surface-pro-11-x1e-oled", Basename: "x1e80100-microsoft-denali-oled.dtb", Path: "usr/lib/firmware/" + abi + "/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb", CompatibleStrings: []string{"microsoft,denali"}, SHA256: strings.Repeat("e", 64), EmbeddedMatches: 1, Selectors: []kernel.DeviceTreeSelector{{Kind: kernel.DeviceTreeSelectorCompatible, Value: "microsoft,denali"}, {Kind: kernel.DeviceTreeSelectorHWID, Value: "11111111-1111-5111-8111-111111111111"}}, Required: true},
			{Device: "surface-pro-11-x1p-lcd", Basename: "x1p64100-microsoft-denali.dtb", Path: "usr/lib/firmware/" + abi + "/device-tree/qcom/x1p64100-microsoft-denali.dtb", CompatibleStrings: []string{"microsoft,denali-x1p"}, SHA256: strings.Repeat("f", 64), EmbeddedMatches: 1, Selectors: []kernel.DeviceTreeSelector{{Kind: kernel.DeviceTreeSelectorCompatible, Value: "microsoft,denali-x1p"}, {Kind: kernel.DeviceTreeSelectorHWID, Value: "22222222-2222-5222-8222-222222222222"}}, Required: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestFile, err := os.Create(filepath.Join(directory, "lexr-kernel-bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.WriteJSON(manifestFile); err != nil {
		_ = manifestFile.Close()
		t.Fatal(err)
	}
	if err := manifestFile.Close(); err != nil {
		t.Fatal(err)
	}

	bundle, err := (&ImageManager{}).resolveBundle(context.Background(), CreateImageRequest{
		KernelDirectory: directory,
	}, "unused-cache")
	if err != nil {
		t.Fatalf("resolveBundle(local) error = %v", err)
	}
	if bundle.ABI != abi || len(bundle.Packages) != 2 {
		t.Fatalf("resolveBundle(local) = ABI %q, packages %#v", bundle.ABI, bundle.Packages)
	}
	for _, role := range []kernel.PackageRole{kernel.RoleImage, kernel.RoleModules} {
		pkg, ok := bundle.Package(role)
		if !ok || pkg.SHA256 == "" || pkg.Path == "" {
			t.Errorf("resolved local package %q = %#v, found %v", role, pkg, ok)
		}
	}
}

// TestSafePathComponent verifies release references become bounded cache path
// components and traversal-like or empty inputs fall back to a safe default.
func TestSafePathComponent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{input: "latest", want: "latest"},
		{input: "refs/tags/sp11-v1", want: "refs-tags-sp11-v1"},
		{input: "feature\\kernel", want: "feature-kernel"},
		{input: "../../", want: "latest"},
		{input: "", want: "latest"},
	}
	for _, test := range tests {
		if got := safePathComponent(test.input); got != test.want {
			t.Errorf("safePathComponent(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

// TestResolveCompanionRequiresSourceForOfflinePayloads verifies userspace files
// cannot be requested without the CLI and corresponding source that operate them.
func TestResolveCompanionRequiresSourceForOfflinePayloads(t *testing.T) {
	t.Parallel()

	_, err := (&ImageManager{}).resolveCompanion(context.Background(), CreateImageRequest{
		CompanionUserspace: []string{"iptsd"},
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "--companion-source-dir") {
		t.Fatalf("resolveCompanion() error = %v, want source-directory requirement", err)
	}
}

// TestResolveCompanionPreservesBuildIdentity verifies an explicitly selected
// source tree carries the same version, revision, and date into generic staging.
func TestResolveCompanionPreservesBuildIdentity(t *testing.T) {
	t.Parallel()

	runner := &companionProbeRunner{}
	request, err := newCompanionTestManager(runner).resolveCompanion(context.Background(), CreateImageRequest{
		CompanionSourceDirectory: ".",
		Output:                   filepath.Join(t.TempDir(), "lexr.iso"),
		ToolVersion:              "1.2.3",
		ToolCommit:               "abcdef",
		ToolBuildDate:            "2026-08-30T12:00:00Z",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wantSource, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	if request.SourceDirectory != wantSource || request.Version != "1.2.3" ||
		request.Commit != "abcdef" || request.BuildDate != "2026-08-30T12:00:00Z" {
		t.Fatalf("resolveCompanion() = %#v", request)
	}
	if request.UserspaceCatalog == nil {
		t.Fatal("resolveCompanion() did not load the userspace catalogue for a core-only bundle")
	}
	if len(runner.commands) != 1 || runner.commands[0].Name != "go" ||
		!reflect.DeepEqual(runner.commands[0].Args, []string{"version"}) {
		t.Fatalf("toolchain probes = %#v", runner.commands)
	}
}

// TestResolveCompanionDerivesLocalGitIdentity verifies an untagged development
// CLI records the selected source revision and canonical UTC commit timestamp.
func TestResolveCompanionDerivesLocalGitIdentity(t *testing.T) {
	t.Parallel()

	const revision = "0123456789abcdef0123456789abcdef01234567"
	source := "/source/lexr"
	runner := &companionProbeRunner{responses: map[string][]byte{
		probeCommandKey(platform.Command{
			Name: "git", Args: []string{"-C", source, "rev-parse", "HEAD"},
		}): []byte(revision + "\n"),
		probeCommandKey(platform.Command{
			Name: "git", Args: []string{"-C", source, "show", "-s", "--format=%cI", revision},
		}): []byte("2026-08-30T14:30:00+01:00\n"),
		probeCommandKey(platform.Command{
			Name: "git", Args: []string{"-C", source, "status", "--porcelain=v1", "--untracked-files=all", "--", "."},
		}): []byte{},
	}}
	request, err := newCompanionTestManager(runner).resolveCompanion(context.Background(), CreateImageRequest{
		CompanionSourceDirectory: source,
		Output:                   filepath.Join(t.TempDir(), "lexr.iso"),
		ToolVersion:              "dev",
		ToolCommit:               "unknown",
		ToolBuildDate:            "unknown",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if request.Commit != revision || request.BuildDate != "2026-08-30T13:30:00Z" {
		t.Fatalf("derived companion identity = %q/%q", request.Commit, request.BuildDate)
	}
}

// TestResolveCompanionKeepsGeneratedPathsOutsideSource verifies the image,
// sidecars, and explicit workspace cannot dirty the source before snapshotting.
func TestResolveCompanionKeepsGeneratedPathsOutsideSource(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	outside := t.TempDir()
	imageManager := newCompanionTestManager(&companionProbeRunner{})
	base := CreateImageRequest{
		CompanionSourceDirectory: source,
		Output:                   filepath.Join(outside, "lexr.iso"),
	}
	if _, err := imageManager.resolveCompanion(context.Background(), base, outside); err != nil {
		t.Fatalf("resolveCompanion(outside paths) error = %v", err)
	}

	insideOutput := base
	insideOutput.Output = filepath.Join(source, "lexr.iso")
	if _, err := imageManager.resolveCompanion(context.Background(), insideOutput, outside); err == nil ||
		!strings.Contains(err.Error(), "output and its sidecars must be outside") {
		t.Fatalf("resolveCompanion(inside output) error = %v", err)
	}

	insideWorkspace := base
	insideWorkspace.WorkspaceRoot = filepath.Join(source, "work")
	if _, err := imageManager.resolveCompanion(context.Background(), insideWorkspace, outside); err == nil ||
		!strings.Contains(err.Error(), "--workspace-dir must be outside") {
		t.Fatalf("resolveCompanion(inside workspace) error = %v", err)
	}
}

// TestResolveCompanionUsesCompiledOfflineAllowlist verifies editable catalogue
// policy alone cannot authorise a component for redistribution on image media.
func TestResolveCompanionUsesCompiledOfflineAllowlist(t *testing.T) {
	t.Parallel()

	imageManager := newCompanionTestManager(&companionProbeRunner{})
	for _, selector := range []string{"audio", "camera"} {
		_, err := imageManager.resolveCompanion(context.Background(), CreateImageRequest{
			CompanionSourceDirectory: "/source/lexr",
			CompanionUserspace:       []string{selector},
		}, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "not approved for offline companion inclusion") {
			t.Errorf("resolveCompanion(%s) error = %v", selector, err)
		}
	}
}

// TestResolveCompanionRejectsRecommendedExpansion verifies offline inclusion is
// reviewed component by component instead of inheriting an install convenience set.
func TestResolveCompanionRejectsRecommendedExpansion(t *testing.T) {
	t.Parallel()

	imageManager := newCompanionTestManager(&companionProbeRunner{})
	_, err := imageManager.resolveCompanion(context.Background(), CreateImageRequest{
		CompanionSourceDirectory: "/source/lexr",
		CompanionUserspace:       []string{"recommended"},
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "must name each") {
		t.Fatalf("resolveCompanion(recommended) error = %v", err)
	}
}

// TestResolveCompanionRejectsDuplicateAliasesBeforeDownloads verifies aliases
// cannot cause the same offline release to be fetched twice.
func TestResolveCompanionRejectsDuplicateAliasesBeforeDownloads(t *testing.T) {
	t.Parallel()

	_, err := newCompanionTestManager(&companionProbeRunner{}).resolveCompanion(
		context.Background(),
		CreateImageRequest{
			CompanionSourceDirectory: "/source/lexr",
			CompanionUserspace:       []string{"iptsd", userspacemanager.IPTSDComponent},
		},
		t.TempDir(),
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate companion userspace component") {
		t.Fatalf("resolveCompanion(duplicate aliases) error = %v", err)
	}
}

// TestResolveCompanionRequiresWorkingGo verifies the optional source workflow
// reports its additional host prerequisite before catalogue or download work.
func TestResolveCompanionRequiresWorkingGo(t *testing.T) {
	t.Parallel()

	runner := &companionProbeRunner{err: errors.New("go executable not found")}
	_, err := (&ImageManager{CompanionRunner: runner}).resolveCompanion(
		context.Background(),
		CreateImageRequest{CompanionSourceDirectory: "/source/lexr"},
		t.TempDir(),
	)
	if err == nil || !strings.Contains(err.Error(), "working Go toolchain") {
		t.Fatalf("resolveCompanion(missing Go) error = %v", err)
	}
}
