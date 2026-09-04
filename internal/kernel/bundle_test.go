package kernel

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const (
	// testBundleABI is the coherent Surface ABI used by bundle fixtures.
	testBundleABI = "7.2.0-jg-0sp11v19-qcom-x1e"
	// testBundleVersion is the Debian version used by every fixture package.
	testBundleVersion = "7.2.0-jg-0sp11v19"
)

// TestNewBundleSeparatesRequestedAndEffectiveDelivery covers valid policy/result combinations.
func TestNewBundleSeparatesRequestedAndEffectiveDelivery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		requested RequestedBootImageMode
		effective DTBDelivery
		support   bool
		wantError string
	}{
		{name: "source produced embedded", requested: RequestedBootImageModeSource, effective: DTBDeliveryEmbedded},
		{name: "source produced external", requested: RequestedBootImageModeSource, effective: DTBDeliveryExternalRequired, support: true},
		{name: "stubble produced embedded", requested: RequestedBootImageModeStubble, effective: DTBDeliveryEmbedded},
		{name: "nostubble produced external", requested: RequestedBootImageModeNoStubble, effective: DTBDeliveryExternalRequired, support: true},
		{name: "stubble produced external", requested: RequestedBootImageModeStubble, effective: DTBDeliveryExternalRequired, support: true, wantError: "stubble request requires embedded"},
		{name: "nostubble produced embedded", requested: RequestedBootImageModeNoStubble, effective: DTBDeliveryEmbedded, wantError: "nostubble request requires external-required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := validBundleOptions(test.requested, test.effective, test.support)
			bundle, err := NewBundle(options)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("NewBundle() error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if bundle.SchemaVersion != BundleSchemaVersion || bundle.RequestedBootImageMode != test.requested || bundle.EffectiveDTBDelivery != test.effective {
				t.Fatalf("delivery contract = %#v", bundle)
			}
		})
	}
}

// TestNewBundleCanonicalisesAndOwnsInventory proves deterministic deep copying.
func TestNewBundleCanonicalisesAndOwnsInventory(t *testing.T) {
	t.Parallel()
	options := validBundleOptions(RequestedBootImageModeSource, DTBDeliveryEmbedded, false)
	options.Packages[0], options.Packages[1] = options.Packages[1], options.Packages[0]
	options.DeviceTrees[0], options.DeviceTrees[1] = options.DeviceTrees[1], options.DeviceTrees[0]
	options.DeviceTrees[1].CompatibleStrings = []string{"microsoft,denali", "qcom,x1e80100"}
	options.DeviceTrees[1].Selectors = []DeviceTreeSelector{
		{Kind: DeviceTreeSelectorCompatible, Value: "microsoft,denali"},
		{Kind: DeviceTreeSelectorHWID, Value: "11111111-1111-5111-8111-111111111111"},
	}

	bundle, err := NewBundle(options)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Packages[0].Role != RoleImage || bundle.Packages[1].Role != RoleModules {
		t.Fatalf("packages are not canonical: %#v", bundle.Packages)
	}
	if bundle.DeviceTrees[0].Device != "surface-pro-11-x1e-oled" || bundle.DeviceTrees[1].Device != "surface-pro-11-x1p-lcd" {
		t.Fatalf("device trees are not canonical: %#v", bundle.DeviceTrees)
	}
	if got := bundle.DeviceTrees[0].CompatibleStrings; !reflect.DeepEqual(got, []string{"microsoft,denali", "qcom,x1e80100"}) {
		t.Fatalf("compatible strings = %#v", got)
	}
	if got := bundle.DeviceTrees[0].Selectors; !reflect.DeepEqual(got, []DeviceTreeSelector{
		{Kind: DeviceTreeSelectorCompatible, Value: "microsoft,denali"},
		{Kind: DeviceTreeSelectorHWID, Value: "11111111-1111-5111-8111-111111111111"},
	}) {
		t.Fatalf("selectors = %#v", got)
	}
	options.DeviceTrees[1].CompatibleStrings[0] = "changed,compatible"
	options.DeviceTrees[1].Selectors[0].Value = "changed"
	if bundle.DeviceTrees[0].CompatibleStrings[0] != "microsoft,denali" || bundle.DeviceTrees[0].Selectors[0].Value != "microsoft,denali" {
		t.Fatal("bundle retained aliases into caller-owned inventory")
	}
}

// TestNewBundleValidatesDetailedSelectionProvenance proves executable identities
// and source records are deep-copied and must agree with the DTB inventory.
func TestNewBundleValidatesDetailedSelectionProvenance(t *testing.T) {
	t.Parallel()
	options := validBundleOptions(RequestedBootImageModeStubble, DTBDeliveryEmbedded, false)
	options.DeviceTrees[0].Selectors = []DeviceTreeSelector{
		{Kind: DeviceTreeSelectorCompatible, Value: "microsoft,denali"},
		{Kind: DeviceTreeSelectorHWID, Value: "11111111-1111-5111-8111-111111111111"},
	}
	options.DeviceTrees[1].Selectors = []DeviceTreeSelector{
		{Kind: DeviceTreeSelectorCompatible, Value: "microsoft,denali-x1p"},
		{Kind: DeviceTreeSelectorHWID, Value: "22222222-2222-5222-8222-222222222222"},
	}
	options.DTBSelectionProvenance = detailedBundleSelectionProvenance()
	bundle, err := NewBundle(options)
	if err != nil {
		t.Fatal(err)
	}
	options.DTBSelectionProvenance.Selections[0].Records[0].HWIDs[0] = "changed"
	if bundle.DTBSelectionProvenance.Selections[0].Records[0].HWIDs[0] != "11111111-1111-5111-8111-111111111111" {
		t.Fatal("bundle retained aliases into detailed selection provenance")
	}

	invalid := validBundleOptions(RequestedBootImageModeStubble, DTBDeliveryEmbedded, false)
	invalid.DeviceTrees = bundle.DeviceTrees
	invalid.DTBSelectionProvenance = detailedBundleSelectionProvenance()
	invalid.DTBSelectionProvenance.HelperSHA256 = ""
	if _, err := NewBundle(invalid); err == nil || !strings.Contains(err.Error(), "Stubble helper") {
		t.Fatalf("missing helper identity error = %v", err)
	}
	invalid.DTBSelectionProvenance = detailedBundleSelectionProvenance()
	invalid.DTBSelectionProvenance.Selections[0].Records[0].Compatible = "vendor,other"
	if _, err := NewBundle(invalid); err == nil || !strings.Contains(err.Error(), "primary compatible") {
		t.Fatalf("selection disagreement error = %v", err)
	}
	invalid = validBundleOptions(RequestedBootImageModeStubble, DTBDeliveryEmbedded, false)
	invalid.DeviceTrees[0].CompatibleStrings = []string{"vendor,primary", "microsoft,denali"}
	if _, err := NewBundle(invalid); err == nil || !strings.Contains(err.Error(), "primary compatible") {
		t.Fatalf("secondary compatible route error = %v", err)
	}
}

// TestNewBundleValidatesPackageAndInventoryContract covers malformed delivery evidence.
func TestNewBundleValidatesPackageAndInventoryContract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*BundleOptions)
		want   string
	}{
		{name: "missing requested mode", mutate: func(options *BundleOptions) { options.RequestedBootImageMode = "" }, want: "requested boot-image mode"},
		{name: "missing effective delivery", mutate: func(options *BundleOptions) { options.EffectiveDTBDelivery = "" }, want: "effective DTB delivery"},
		{name: "external lacks support", mutate: func(options *BundleOptions) {
			options.EffectiveDTBDelivery = DTBDeliveryExternalRequired
			for index := range options.DeviceTrees {
				options.DeviceTrees[index].EmbeddedMatches = 0
			}
		}, want: "requires one boot-support package"},
		{name: "embedded includes support", mutate: func(options *BundleOptions) {
			options.Packages = append(options.Packages, bootSupportPackage())
		}, want: "must not contain"},
		{name: "embedded lacks selection provenance", mutate: func(options *BundleOptions) {
			options.DTBSelectionProvenance = nil
		}, want: "requires DTB selection provenance"},
		{name: "embedded compact provenance is rejected", mutate: func(options *BundleOptions) {
			options.DTBSelectionProvenance = &DTBSelectionProvenance{Tool: "stubble", Version: "1", DatabaseSHA256: strings.Repeat("f", 64)}
		}, want: "requires detailed Stubble and ukify provenance"},
		{name: "external has selection provenance", mutate: func(options *BundleOptions) {
			options.EffectiveDTBDelivery = DTBDeliveryExternalRequired
			options.EmbeddedDTBCount = 0
			options.Packages = append(options.Packages, bootSupportPackage())
			for index := range options.DeviceTrees {
				options.DeviceTrees[index].EmbeddedMatches = 0
			}
		}, want: "must not contain Stubble selection provenance"},
		{name: "boot support version mismatch", mutate: func(options *BundleOptions) {
			options.RequestedBootImageMode = RequestedBootImageModeNoStubble
			options.EffectiveDTBDelivery = DTBDeliveryExternalRequired
			options.EmbeddedDTBCount = 0
			options.DTBSelectionProvenance = nil
			options.Packages = append(options.Packages, Package{
				Role: RoleBootSupport, Name: "lexr-kernel-boot-support_9.9.9_all.deb",
				SHA256: strings.Repeat("e", 64), Size: 1, Verified: true,
			})
			for index := range options.DeviceTrees {
				options.DeviceTrees[index].EmbeddedMatches = 0
			}
		}, want: "differs from kernel version"},
		{name: "unpaired headers", mutate: func(options *BundleOptions) {
			options.Packages = append(options.Packages, packageForRole(RoleHeaders))
		}, want: "must be supplied together"},
		{name: "bad package digest", mutate: func(options *BundleOptions) { options.Packages[0].SHA256 = "ABC" }, want: "non-canonical SHA-256"},
		{name: "bad package size", mutate: func(options *BundleOptions) { options.Packages[0].Size = 0 }, want: "non-positive size"},
		{name: "duplicate device", mutate: func(options *BundleOptions) { options.DeviceTrees[1].Device = options.DeviceTrees[0].Device }, want: "duplicate device tree"},
		{name: "wrong ABI path", mutate: func(options *BundleOptions) {
			options.DeviceTrees[0].Path = "usr/lib/firmware/other/device-tree/qcom/test.dtb"
		}, want: "invalid package path"},
		{name: "basename mismatch", mutate: func(options *BundleOptions) { options.DeviceTrees[0].Basename = "other.dtb" }, want: "basename does not match"},
		{name: "duplicate compatible", mutate: func(options *BundleOptions) {
			options.DeviceTrees[0].CompatibleStrings = []string{"microsoft,denali", "microsoft,denali"}
		}, want: "compatible strings"},
		{name: "duplicate selector", mutate: func(options *BundleOptions) {
			options.DeviceTrees[0].Selectors = append(options.DeviceTrees[0].Selectors, options.DeviceTrees[0].Selectors[0])
		}, want: "selectors are duplicate"},
		{name: "compatible selector absent from DTB", mutate: func(options *BundleOptions) {
			options.DeviceTrees[0].Selectors[0].Value = "vendor,undeclared"
		}, want: "compatible selector is absent"},
		{name: "ambiguous cross-device route", mutate: func(options *BundleOptions) {
			options.RequestedBootImageMode = RequestedBootImageModeNoStubble
			options.EffectiveDTBDelivery = DTBDeliveryExternalRequired
			options.EmbeddedDTBCount = 0
			options.DTBSelectionProvenance = nil
			options.Packages = append(options.Packages, bootSupportPackage())
			for index := range options.DeviceTrees {
				options.DeviceTrees[index].EmbeddedMatches = 0
			}
			shared := DeviceTreeSelector{Kind: DeviceTreeSelectorCompatible, Value: "microsoft,denali"}
			options.DeviceTrees[0].Selectors = []DeviceTreeSelector{shared}
			options.DeviceTrees[1].CompatibleStrings = append(options.DeviceTrees[1].CompatibleStrings, shared.Value)
			options.DeviceTrees[1].Selectors = []DeviceTreeSelector{shared}
		}, want: "same selection route"},
		{name: "required embedded missing", mutate: func(options *BundleOptions) { options.DeviceTrees[0].EmbeddedMatches = 0 }, want: "does not have exactly one embedded match"},
		{name: "no required profile", mutate: func(options *BundleOptions) {
			for index := range options.DeviceTrees {
				options.DeviceTrees[index].Required = false
			}
		}, want: "no device tree required"},
		{name: "external has embedded", mutate: func(options *BundleOptions) {
			options.EffectiveDTBDelivery = DTBDeliveryExternalRequired
			options.Packages = append(options.Packages, bootSupportPackage())
		}, want: "unexpectedly has an embedded match"},
		{name: "unattributed embedded section", mutate: func(options *BundleOptions) {
			options.EmbeddedDTBCount++
		}, want: "but 2 are attributable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := validBundleOptions(RequestedBootImageModeSource, DTBDeliveryEmbedded, false)
			test.mutate(&options)
			_, err := NewBundle(options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewBundle() error = %v, want %q", err, test.want)
			}
		})
	}
}

// TestNewBundleRequiresMatchingRuntimePackages rejects incomplete or mixed runtimes.
func TestNewBundleRequiresMatchingRuntimePackages(t *testing.T) {
	t.Parallel()
	options := validBundleOptions(RequestedBootImageModeSource, DTBDeliveryEmbedded, false)
	options.Packages[1] = Package{
		Name:   "linux-modules-7.2.1-sp11-qcom-x1e_7.2.1-sp11_arm64.deb",
		SHA256: strings.Repeat("b", 64), Size: 1,
	}
	_, err := NewBundle(options)
	if err == nil || !strings.Contains(err.Error(), "mixed kernel") {
		t.Fatalf("expected mixed kernel error, got %v", err)
	}
}

// TestNewBundleRejectsGenericARM64Kernel enforces the Surface kernel flavour.
func TestNewBundleRejectsGenericARM64Kernel(t *testing.T) {
	t.Parallel()
	options := validBundleOptions(RequestedBootImageModeSource, DTBDeliveryEmbedded, false)
	options.Packages = []Package{
		{Role: RoleImage, Name: "linux-image-7.2.0-generic_7.2.0_arm64.deb", SHA256: strings.Repeat("a", 64), Size: 1},
		{Role: RoleModules, Name: "linux-modules-7.2.0-generic_7.2.0_arm64.deb", SHA256: strings.Repeat("b", 64), Size: 1},
	}
	if _, err := NewBundle(options); err == nil {
		t.Fatal("NewBundle() accepted a generic ARM64 kernel")
	}
}

// TestParsePackageNameAcceptsGenericBootSupport covers the architecture-independent support role.
func TestParsePackageNameAcceptsGenericBootSupport(t *testing.T) {
	t.Parallel()
	role, abi, version, err := ParsePackageName("lexr-kernel-boot-support_2.4.1-1_all.deb")
	if err != nil {
		t.Fatal(err)
	}
	if role != RoleBootSupport || abi != "" || version != "2.4.1-1" {
		t.Fatalf("boot support identity = role %q ABI %q version %q", role, abi, version)
	}
}

// TestSchemaOneBundleRemainsDecodableForExplicitMigration preserves deliberate legacy reading.
func TestSchemaOneBundleRemainsDecodableForExplicitMigration(t *testing.T) {
	t.Parallel()
	data := []byte(`{
  "schema_version": 1,
  "release": "legacy",
  "abi": "7.2.0-jg-0sp11v19-qcom-x1e",
  "version": "7.2.0-jg-0sp11v19",
  "architecture": "arm64",
  "packages": [],
  "device_trees": [{"device":"surface-pro-11-x1e-oled","path":"qcom/legacy.dtb"}]
}`)
	var bundle Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.SchemaVersion != 1 || bundle.DeviceTrees[0].Path != "qcom/legacy.dtb" || bundle.RequestedBootImageMode != "" || bundle.EffectiveDTBDelivery != "" {
		t.Fatalf("legacy bundle = %#v", bundle)
	}
}

// validBundleOptions returns one complete contract for focused mutation tests.
func validBundleOptions(requested RequestedBootImageMode, effective DTBDelivery, support bool) BundleOptions {
	packages := []Package{packageForRole(RoleModules), packageForRole(RoleImage)}
	if support {
		packages = append(packages, bootSupportPackage())
	}
	embeddedMatches := 1
	if effective == DTBDeliveryExternalRequired {
		embeddedMatches = 0
	}
	options := BundleOptions{
		Release:                "sp11-v19",
		Repository:             "ooaklee/linux-surface-pro-11-oe",
		RequestedBootImageMode: requested,
		EffectiveDTBDelivery:   effective,
		EmbeddedDTBCount:       2 * embeddedMatches,
		DTBSelectionProvenance: detailedBundleSelectionProvenance(),
		Packages:               packages,
		DeviceTrees: []DeviceTree{
			{
				Device: "surface-pro-11-x1e-oled", Basename: "x1e80100-microsoft-denali-oled.dtb",
				Path:              "usr/lib/firmware/" + testBundleABI + "/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb",
				CompatibleStrings: []string{"microsoft,denali", "qcom,x1e80100"}, SHA256: strings.Repeat("c", 64),
				EmbeddedMatches: embeddedMatches, Selectors: []DeviceTreeSelector{
					{Kind: DeviceTreeSelectorCompatible, Value: "microsoft,denali"},
					{Kind: DeviceTreeSelectorHWID, Value: "11111111-1111-5111-8111-111111111111"},
				}, Required: true,
			},
			{
				Device: "surface-pro-11-x1p-lcd", Basename: "x1p64100-microsoft-denali.dtb",
				Path:              "usr/lib/firmware/" + testBundleABI + "/device-tree/qcom/x1p64100-microsoft-denali.dtb",
				CompatibleStrings: []string{"microsoft,denali-x1p", "qcom,x1p64100"}, SHA256: strings.Repeat("d", 64),
				EmbeddedMatches: embeddedMatches, Selectors: []DeviceTreeSelector{
					{Kind: DeviceTreeSelectorCompatible, Value: "microsoft,denali-x1p"},
					{Kind: DeviceTreeSelectorHWID, Value: "22222222-2222-5222-8222-222222222222"},
				}, Required: true,
			},
		},
	}
	if effective == DTBDeliveryExternalRequired {
		options.DTBSelectionProvenance = nil
	}
	return options
}

// detailedBundleSelectionProvenance returns complete executable and selector evidence.
func detailedBundleSelectionProvenance() *DTBSelectionProvenance {
	return &DTBSelectionProvenance{
		Tool: "stubble", Version: "1.2.3-1", DatabaseSHA256: strings.Repeat("f", 64),
		StubSHA256: strings.Repeat("1", 64), HelperSHA256: strings.Repeat("2", 64), SBATSHA256: strings.Repeat("3", 64),
		UKifyTool: "ukify", UKifyPackage: "systemd-ukify", UKifyVersion: "258.1-1", UKifySHA256: strings.Repeat("4", 64),
		Selections: []DeviceTreeSelectionEvidence{
			{Device: "surface-pro-11-x1e-oled", Records: []DTBSelectionRecord{{
				Source: "hwids", Compatible: "microsoft,denali", HWIDs: []string{"11111111-1111-5111-8111-111111111111"},
			}}},
			{Device: "surface-pro-11-x1p-lcd", Records: []DTBSelectionRecord{{
				Source: "hwids", Compatible: "microsoft,denali-x1p", HWIDs: []string{"22222222-2222-5222-8222-222222222222"},
			}}},
		},
	}
}

// packageForRole returns one immutable package fixture for a kernel role.
func packageForRole(role PackageRole) Package {
	name := ""
	switch role {
	case RoleImage:
		name = "linux-image-" + testBundleABI + "_" + testBundleVersion + "_arm64.deb"
	case RoleModules:
		name = "linux-modules-" + testBundleABI + "_" + testBundleVersion + "_arm64.deb"
	case RoleHeaders:
		name = "linux-headers-" + testBundleABI + "_" + testBundleVersion + "_arm64.deb"
	case RoleCommonHeaders:
		name = "linux-qcom-x1e-headers-7.2.0-jg-0sp11v19_" + testBundleVersion + "_all.deb"
	}
	return Package{Role: role, Name: name, SHA256: strings.Repeat("a", 64), Size: 1, Verified: true}
}

// bootSupportPackage returns version-matched external-delivery support.
func bootSupportPackage() Package {
	return Package{
		Role: RoleBootSupport, Name: "lexr-kernel-boot-support_" + testBundleVersion + "_all.deb",
		SHA256: strings.Repeat("e", 64), Size: 1, Verified: true,
	}
}
