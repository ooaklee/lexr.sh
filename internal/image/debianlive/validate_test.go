package debianlive

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// debianManifestFixture produces canonical provenance with a non-v23 kernel so
// validation cannot accidentally turn the initial test baseline into a gate.
func debianManifestFixture(t *testing.T) imagecontract.Manifest {
	t.Helper()
	const version = "7.2.0-jg-0sp11v24"
	const abi = version + "-qcom-x1e"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("test")))
	var packages []kernel.Package
	for _, role := range []kernel.PackageRole{kernel.RoleImage, kernel.RoleModules, kernel.RoleBootSupport} {
		name := "linux-" + string(role) + "-" + abi + "_" + version + "_arm64.deb"
		if role == kernel.RoleBootSupport {
			name = "lexr-kernel-boot-support_" + version + "_all.deb"
		}
		packages = append(packages, kernel.Package{Role: role, Name: name, SHA256: digest, Size: 4, Verified: true})
	}
	var trees []kernel.DeviceTree
	for _, profile := range []struct{ device, basename, compatible string }{
		{"surface-pro-11-x1e-oled", "x1e80100-microsoft-denali-oled.dtb", "microsoft,denali"},
		{"surface-pro-11-x1p-lcd", "x1p64100-microsoft-denali.dtb", "microsoft,denali-x1p"},
	} {
		trees = append(trees, kernel.DeviceTree{Device: profile.device, Basename: profile.basename, Path: "usr/lib/firmware/" + abi + "/device-tree/qcom/" + profile.basename, SHA256: digest, Required: profile.device == "surface-pro-11-x1e-oled", CompatibleStrings: []string{profile.compatible}, Selectors: []kernel.DeviceTreeSelector{{Kind: kernel.DeviceTreeSelectorCompatible, Value: profile.compatible}}})
	}
	bundle, err := kernel.NewBundle(kernel.BundleOptions{Release: "sp11-test", Repository: "ooaklee/linux-surface-pro-11-oe", RequestedBootImageMode: kernel.RequestedBootImageModeNoStubble, EffectiveDTBDelivery: kernel.DTBDeliveryExternalRequired, Packages: packages, DeviceTrees: trees})
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	for _, name := range []string{"live-vmlinuz", "live-initrd", "grubaa64.efi", "esp.img", "disk-info", grubSupportDirectory + "/" + grubSupportDebName, "sp11/dtb/" + trees[0].Basename, "sp11/dtb/" + trees[1].Basename} {
		path := filepath.Join(workspace, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "live-uuid"), []byte("12345678-1234-1234-1234-123456789abc"+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	layout := sourceLayout{liveDirectory: "live"}
	layout.kernel, layout.initrd = layout.member("vmlinuz"), layout.member("initrd.img")
	manifest, err := buildManifest(Request{Bundle: bundle, ToolVersion: "v0.3.1-dev"}, layout, workspace, imagecontract.ArtifactRecord{Path: "source.iso", SHA256: inspectedSourceISOHash, Size: inspectedSourceISOSize}, companion.Absent(companion.OmissionReasonNotRequested))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

// TestDebianManifestRequiresAllBootContracts checks canonical round-tripping and
// rejects omitted, duplicate, cross-distro, unsafe and mismatched evidence.
func TestDebianManifestRequiresAllBootContracts(t *testing.T) {
	valid := debianManifestFixture(t)
	if _, _, _, err := validateManifest(valid); err != nil {
		t.Fatal(err)
	}
	for _, testcase := range []struct {
		name   string
		change func(*imagecontract.Manifest)
	}{
		{"wrong source snapshot", func(m *imagecontract.Manifest) { m.SourceImage.SHA256 = strings.Repeat("a", 64) }},
		{"Casper selectors", func(m *imagecontract.Manifest) { m.BootArguments[0] = "boot=casper" }},
		{"wrong adapter", func(m *imagecontract.Manifest) { m.Adapter = "ubuntu-casper" }},
		{"missing EFI contract", func(m *imagecontract.Manifest) { m.MediaDiscovery.Evidence = m.MediaDiscovery.Evidence[:5] }},
		{"duplicate role", func(m *imagecontract.Manifest) { m.MediaDiscovery.Evidence[5] = m.MediaDiscovery.Evidence[4] }},
		{"different initrd UUID", func(m *imagecontract.Manifest) {
			m.MediaDiscovery.Evidence[1].Value = "99999999-1234-1234-1234-123456789abc"
		}},
		{"media traversal", func(m *imagecontract.Manifest) {
			m.MediaDiscovery.Evidence[3].Path = "../other"
			m.MediaDiscovery.Evidence[3].Value = "/../other"
		}},
		{"wrong kernel path", func(m *imagecontract.Manifest) { m.BootArtifacts.Kernel.Path = "casper/vmlinuz" }},
		{"wrong DTB", func(m *imagecontract.Manifest) { m.BootArtifacts.DTBs[0].SHA256 = strings.Repeat("a", 64) }},
		{"missing panel", func(m *imagecontract.Manifest) { m.BootArtifacts.DTBs = m.BootArtifacts.DTBs[:1] }},
		{"unbound package", func(m *imagecontract.Manifest) { m.KernelBundle.Packages[0].Path = "sp11/kernel/elsewhere.deb" }},
		{"DSP blacklist", func(m *imagecontract.Manifest) {
			m.BootArguments = append(m.BootArguments, "modprobe.blacklist=qcom_q6v5_pas")
		}},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			encoded, err := json.Marshal(valid)
			if err != nil {
				t.Fatal(err)
			}
			var modified imagecontract.Manifest
			if err := json.Unmarshal(encoded, &modified); err != nil {
				t.Fatal(err)
			}
			testcase.change(&modified)
			if _, _, _, err := validateManifest(modified); err == nil {
				t.Fatal("invalid media contract accepted")
			}
		})
	}
}

// TestDebianInstalledInitramfsCannotRequireTheUSB detects a live-initramfs mix-up
// and wrong-ABI modules while allowing the separate installed-system image.
func TestDebianInstalledInitramfsCannotRequireTheUSB(t *testing.T) {
	const abi = "7.2.0-jg-0sp11v24-qcom-x1e"
	installed := "usr/lib/modules/" + abi + "/kernel/drivers/usb/host/xhci-pci.ko\n"
	live := installed + "scripts/live\nusr/bin/live-boot\nusr/lib/live/boot/9990-misc-helpers.sh\nconf/uuid.conf\n"
	if err := validateInitrdMembers(installed, abi, false); err != nil {
		t.Fatal(err)
	}
	if err := validateInitrdMembers(live, abi, true); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{live, installed + "usr/bin/live-boot\n", strings.ReplaceAll(installed, abi, "6.17.9-generic"), "scripts/local\n"} {
		if err := validateInitrdMembers(bad, abi, false); err == nil {
			t.Fatalf("invalid installed initramfs accepted: %s", bad)
		}
	}
}
