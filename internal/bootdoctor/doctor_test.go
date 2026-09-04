package bootdoctor

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel/install"
)

const (
	// doctorLegacyABI is a high-generation ABI on the older patch line.
	doctorLegacyABI = "7.2.0-jg-0sp11v20-qcom-x1e"
	// doctorTargetABI is a low-generation ABI on the newer patch line.
	doctorTargetABI = "7.2.2-jg-0sp11v2-qcom-x1e"
)

// writeBootDoctorFile creates one regular fixture file and all of its parents.
func writeBootDoctorFile(t *testing.T, root, relative, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeBootDoctorEmbeddedImage writes a bounded AArch64 COFF fixture with one
// .dtbauto section for every supplied payload.
func writeBootDoctorEmbeddedImage(t *testing.T, path string, payloads ...[]byte) {
	t.Helper()
	const (
		coffHeaderSize    = 20
		sectionHeaderSize = 40
		rawSectionSize    = 512
	)
	image := make([]byte, coffHeaderSize+len(payloads)*sectionHeaderSize+len(payloads)*rawSectionSize)
	binary.LittleEndian.PutUint16(image[0:2], 0xaa64)
	binary.LittleEndian.PutUint16(image[2:4], uint16(len(payloads)))
	for index, payload := range payloads {
		if len(payload) == 0 || len(payload) > rawSectionSize {
			t.Fatalf("embedded DTB fixture payload %d has invalid size %d", index, len(payload))
		}
		headerOffset := coffHeaderSize + index*sectionHeaderSize
		rawOffset := coffHeaderSize + len(payloads)*sectionHeaderSize + index*rawSectionSize
		section := image[headerOffset : headerOffset+sectionHeaderSize]
		copy(section[:8], ".dtbauto")
		binary.LittleEndian.PutUint32(section[8:12], uint32(len(payload)))
		binary.LittleEndian.PutUint32(section[16:20], rawSectionSize)
		binary.LittleEndian.PutUint32(section[20:24], uint32(rawOffset))
		copy(image[rawOffset:rawOffset+rawSectionSize], payload)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatal(err)
	}
}

// embeddedBootDoctorFixture creates one target entry with no external
// devicetree directive and both supported same-ABI firmware DTBs.
func embeddedBootDoctorFixture(t *testing.T, payloads ...[]byte) string {
	t.Helper()
	root := t.TempDir()
	writeBootDoctorEmbeddedImage(t, filepath.Join(root, "boot/vmlinuz-"+doctorTargetABI), payloads...)
	writeBootDoctorFile(t, root, "boot/initrd.img-"+doctorTargetABI, "target initramfs")
	writeBootDoctorFile(t, root, "usr/lib/firmware/"+doctorTargetABI+"/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb", "target OLED device tree")
	writeBootDoctorFile(t, root, "usr/lib/firmware/"+doctorTargetABI+"/device-tree/qcom/x1p64100-microsoft-denali.dtb", "target LCD device tree")
	grub := "menuentry 'Ubuntu " + doctorTargetABI + "' {\n" +
		" linux /boot/vmlinuz-" + doctorTargetABI + "\n" +
		" initrd /boot/initrd.img-" + doctorTargetABI + "\n}\n"
	writeBootDoctorFile(t, root, "boot/grub/grub.cfg", grub)
	return root
}

// bootDoctorFixture creates patch-line, default-selection, recovery, stale,
// digest, and retired-hook evidence beneath an alternate root.
func bootDoctorFixture(t *testing.T, bootDTB string) string {
	t.Helper()
	root := t.TempDir()
	legacyDTB := "legacy patch-line device tree"
	targetDTB := "newer patch-line device tree"
	for _, abi := range []string{doctorLegacyABI, doctorTargetABI} {
		writeBootDoctorFile(t, root, "boot/vmlinuz-"+abi, abi+" kernel")
		writeBootDoctorFile(t, root, "boot/initrd.img-"+abi, abi+" initramfs")
	}
	writeBootDoctorFile(t, root, "usr/lib/firmware/"+doctorLegacyABI+"/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb", legacyDTB)
	writeBootDoctorFile(t, root, "usr/lib/firmware/"+doctorTargetABI+"/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb", targetDTB)
	writeBootDoctorFile(t, root, "boot/sp11-denali.dtb", bootDTB)
	writeBootDoctorFile(t, root, "etc/default/grub", "GRUB_DEFAULT=saved\n")
	writeBootDoctorFile(t, root, "boot/grub/grubenv", "saved_entry=newer-entry\n")
	writeBootDoctorFile(t, root, "usr/local/sbin/sp11-grub-inject-dtb", "retired helper evidence")
	writeBootDoctorFile(t, root, "etc/kernel/postinst.d/zzzz-surface-pro-11-dtb", "retired hook evidence")
	grub := "menuentry 'Legacy " + doctorLegacyABI + "' --id legacy-entry {\n" +
		" linux /boot/vmlinuz-" + doctorLegacyABI + " root=UUID=private unrelated=argument\n" +
		" devicetree /boot/sp11-denali.dtb\n" +
		" initrd /boot/initrd.img-" + doctorLegacyABI + "\n}\n" +
		"menuentry 'Newer " + doctorTargetABI + "' --id newer-entry {\n" +
		" linuxefi /boot/vmlinuz-" + doctorTargetABI + " secret=must-not-appear\n" +
		" devicetree /boot/sp11-denali.dtb\n" +
		" initrdefi /boot/initrd.img-" + doctorTargetABI + "\n}\n" +
		"menuentry 'Newer recovery " + doctorTargetABI + "' --id recovery-entry {\n" +
		" linux /boot/vmlinuz-" + doctorTargetABI + " recovery\n" +
		" devicetree /boot/sp11-denali.dtb\n" +
		" initrd /boot/initrd.img-" + doctorTargetABI + "\n}\n" +
		"menuentry 'Stale at " + root + " 7.1.9-jg-0sp11v99-qcom-x1e' --id stale-entry {\n" +
		" linux /boot/vmlinuz-7.1.9-jg-0sp11v99-qcom-x1e\n" +
		" initrd /boot/initrd.img-7.1.9-jg-0sp11v99-qcom-x1e\n}\n"
	writeBootDoctorFile(t, root, "boot/grub/grub.cfg", grub)
	return root
}

// singleEntryBootDoctorFixture creates one required entry for focused path and
// ABI-stamped device-tree evidence tests.
func singleEntryBootDoctorFixture(t *testing.T, token, bootDTB string) string {
	t.Helper()
	root := t.TempDir()
	writeBootDoctorFile(t, root, "boot/vmlinuz-"+doctorTargetABI, "target kernel")
	writeBootDoctorFile(t, root, "boot/initrd.img-"+doctorTargetABI, "target initramfs")
	writeBootDoctorFile(t, root, "usr/lib/firmware/"+doctorTargetABI+"/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb", "target device tree")
	writeBootDoctorFile(t, root, strings.TrimPrefix(token, "/"), bootDTB)
	grub := "menuentry 'Ubuntu " + doctorTargetABI + "' {\n" +
		" linux /boot/vmlinuz-" + doctorTargetABI + "\n" +
		" devicetree " + token + "\n" +
		" initrd /boot/initrd.img-" + doctorTargetABI + "\n}\n"
	writeBootDoctorFile(t, root, "boot/grub/grub.cfg", grub)
	return root
}

// digestText returns the lowercase SHA-256 digest of one fixture string.
func digestText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// findDoctorCheck returns the first report check with the requested ID and ABI.
func findDoctorCheck(t *testing.T, report Report, id, abi string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id && check.ABI == abi {
			return check
		}
	}
	t.Fatalf("check %s for ABI %s is missing", id, abi)
	return Check{}
}

// TestInspectAttributesSharedDTBPatchLineFirst verifies the complete report
// prefers the newer patch line, resolves saved_entry, and retains exact digests.
func TestInspectAttributesSharedDTBPatchLineFirst(t *testing.T) {
	root := bootDoctorFixture(t, "newer patch-line device tree")
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || report.Default.EntryIndex == nil || *report.Default.EntryIndex != 1 || report.Default.Stale {
		t.Fatalf("default readiness = %#v, ready=%t", report.Default, report.Ready)
	}
	if report.Attribution.SelectedABI != doctorTargetABI || report.Attribution.AttributedABI != doctorTargetABI {
		t.Fatalf("DTB attribution = %#v", report.Attribution)
	}
	wantDigest := digestText("newer patch-line device tree")
	if report.Attribution.BootSHA256 != wantDigest || report.Attribution.InstalledSHA256 != wantDigest {
		t.Fatalf("DTB digests = %#v, want %s", report.Attribution, wantDigest)
	}
	if !report.Hooks.RetiredHelper || !report.Hooks.PostInstallHook || report.Hooks.PostRemoveHook {
		t.Fatalf("legacy hook evidence = %#v", report.Hooks)
	}
	stale := findDoctorCheck(t, report, "grub-entry-artefacts", "7.1.9-jg-0sp11v99-qcom-x1e")
	if stale.State != StateWarn || stale.Required {
		t.Fatalf("stale unrelated entry check = %#v", stale)
	}
	recovery := findDoctorCheck(t, report, "grub-entry-dtb", doctorTargetABI)
	if recovery.State == StateFail {
		t.Fatalf("non-default recovery entry unexpectedly blocked readiness: %#v", recovery)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret=must-not-appear") || strings.Contains(string(encoded), root) {
		t.Fatalf("report exposed an unrelated argument or alternate-root host prefix: %s", encoded)
	}
	if report.PhysicalBootability != physicalBootabilityLimitation {
		t.Fatalf("physical limitation = %q", report.PhysicalBootability)
	}
}

// TestInspectAcceptsDeviceScopedEmbeddedDTB verifies doctor and install share
// the same successful Stubble binding contract and serialised evidence.
func TestInspectAcceptsDeviceScopedEmbeddedDTB(t *testing.T) {
	payload := []byte("target OLED device tree")
	root := embeddedBootDoctorFixture(t, payload)
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled", TargetABI: doctorTargetABI})
	if err != nil {
		t.Fatal(err)
	}
	digest := digestText(string(payload))
	entry := report.Entries[0]
	if !report.Ready || entry.DeviceTreeBoot == nil || entry.DeviceTreeBoot.Mode != install.DeviceTreeBootEmbedded ||
		entry.DeviceTreeBoot.SHA256 != digest || entry.DeviceTreeBoot.GRUBEntryCount != 1 ||
		entry.BootDTBSHA256 != digest || entry.InstalledDTBSHA256 != digest || entry.DTBMatches == nil || !*entry.DTBMatches {
		t.Fatalf("embedded entry evidence = %#v, ready=%t", entry, report.Ready)
	}
	if report.Attribution.DeviceTreeBoot == nil || report.Attribution.DeviceTreeBoot.Mode != install.DeviceTreeBootEmbedded ||
		report.Attribution.DeviceTreeBoot.SHA256 != digest || report.Attribution.AttributedABI != doctorTargetABI {
		t.Fatalf("embedded attribution = %#v", report.Attribution)
	}
	check := findDoctorCheck(t, report, "grub-abi-dtb-consistency", doctorTargetABI)
	if check.State != StatePass || !check.Required || !strings.Contains(check.Detail, "embedded") {
		t.Fatalf("embedded consistency check = %#v", check)
	}
}

// TestInspectAcceptsX1PEmbeddedDTB mirrors the embedded success path for the
// LCD model instead of relying only on negative cross-device coverage.
func TestInspectAcceptsX1PEmbeddedDTB(t *testing.T) {
	payload := []byte("target LCD device tree")
	root := embeddedBootDoctorFixture(t, payload)
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1p-lcd", TargetABI: doctorTargetABI})
	if err != nil {
		t.Fatal(err)
	}
	entry := report.Entries[0]
	if !report.Ready || entry.DeviceTreeBoot == nil || entry.DeviceTreeBoot.Mode != install.DeviceTreeBootEmbedded ||
		entry.DeviceTreeBoot.SHA256 != digestText(string(payload)) || entry.DTBMatches == nil || !*entry.DTBMatches {
		t.Fatalf("X1P embedded entry evidence = %#v, ready=%t", entry, report.Ready)
	}
}

// TestInspectRejectsAnotherDeviceEmbeddedDTB proves explicit OLED diagnosis
// cannot be satisfied by an exact same-ABI LCD payload.
func TestInspectRejectsAnotherDeviceEmbeddedDTB(t *testing.T) {
	root := embeddedBootDoctorFixture(t, []byte("target LCD device tree"))
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled", TargetABI: doctorTargetABI})
	if err != nil {
		t.Fatal(err)
	}
	entry := report.Entries[0]
	if report.Ready || entry.DeviceTreeBoot != nil || entry.BootDTBSHA256 != "" {
		t.Fatalf("wrong-device embedded evidence = %#v, ready=%t", entry, report.Ready)
	}
	check := findDoctorCheck(t, report, "grub-abi-dtb-consistency", doctorTargetABI)
	if check.State != StateFail || !check.Required {
		t.Fatalf("wrong-device consistency check = %#v", check)
	}
}

// TestInspectRejectsAnotherDeviceExternalDTB applies the same explicit model
// boundary to canonical external GRUB paths.
func TestInspectRejectsAnotherDeviceExternalDTB(t *testing.T) {
	root := singleEntryBootDoctorFixture(t, "/boot/x1p64100-microsoft-denali.dtb", "target LCD device tree")
	writeBootDoctorFile(t, root, "usr/lib/firmware/"+doctorTargetABI+"/device-tree/qcom/x1p64100-microsoft-denali.dtb", "target LCD device tree")
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled", TargetABI: doctorTargetABI})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.Entries[0].DeviceTreeBoot != nil {
		t.Fatalf("wrong-device external evidence unexpectedly passed: %#v", report.Entries[0])
	}
	check := findDoctorCheck(t, report, "grub-abi-dtb-consistency", doctorTargetABI)
	if check.State != StateFail || !check.Required {
		t.Fatalf("wrong-device external consistency check = %#v", check)
	}
}

// TestInspectRejectsDuplicateEmbeddedDTBMatches keeps the installer's exactly
// one matching .dtbauto payload invariant in read-only diagnosis.
func TestInspectRejectsDuplicateEmbeddedDTBMatches(t *testing.T) {
	payload := []byte("target OLED device tree")
	root := embeddedBootDoctorFixture(t, payload, payload)
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled", TargetABI: doctorTargetABI})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.Entries[0].DeviceTreeBoot != nil {
		t.Fatalf("duplicate embedded evidence unexpectedly passed: %#v", report.Entries[0])
	}
}

// TestInspectRejectsMissingEmbeddedDTB keeps packaged firmware files from
// becoming boot evidence when no external directive or embedded section exists.
func TestInspectRejectsMissingEmbeddedDTB(t *testing.T) {
	root := embeddedBootDoctorFixture(t, []byte("target OLED device tree"))
	writeBootDoctorFile(t, root, "boot/vmlinuz-"+doctorTargetABI, "raw kernel without embedded device tree")
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled", TargetABI: doctorTargetABI})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.Entries[0].DeviceTreeBoot != nil {
		t.Fatalf("missing embedded evidence unexpectedly passed: %#v", report.Entries[0])
	}
}

// TestInspectRejectsMixedABIBindingModes verifies individually valid normal
// and recovery entries cannot disagree on embedded versus external delivery.
func TestInspectRejectsMixedABIBindingModes(t *testing.T) {
	payload := []byte("target OLED device tree")
	root := embeddedBootDoctorFixture(t, payload)
	writeBootDoctorFile(t, root, "boot/x1e80100-microsoft-denali-oled.dtb", string(payload))
	grub := "menuentry 'Ubuntu " + doctorTargetABI + "' {\n" +
		" linux /boot/vmlinuz-" + doctorTargetABI + "\n" +
		" devicetree /boot/x1e80100-microsoft-denali-oled.dtb\n" +
		" initrd /boot/initrd.img-" + doctorTargetABI + "\n}\n" +
		"menuentry 'Ubuntu " + doctorTargetABI + " recovery' {\n" +
		" linux /boot/vmlinuz-" + doctorTargetABI + " recovery\n" +
		" initrd /boot/initrd.img-" + doctorTargetABI + "\n}\n"
	writeBootDoctorFile(t, root, "boot/grub/grub.cfg", grub)
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled", TargetABI: doctorTargetABI})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.Entries[0].DeviceTreeBoot == nil || report.Entries[1].DeviceTreeBoot == nil {
		t.Fatalf("mixed binding evidence = %#v, ready=%t", report.Entries, report.Ready)
	}
	check := findDoctorCheck(t, report, "grub-abi-dtb-consistency", doctorTargetABI)
	if check.State != StateFail || !check.Required {
		t.Fatalf("mixed binding consistency check = %#v", check)
	}
}

// TestInspectFailsAfterReportingDefaultDTBMismatch verifies wrong-patch-line
// bytes make the effective default not ready while preserving both digests.
func TestInspectFailsAfterReportingDefaultDTBMismatch(t *testing.T) {
	root := bootDoctorFixture(t, "legacy patch-line device tree")
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.Attribution.AttributedABI != doctorLegacyABI {
		t.Fatalf("mismatched report readiness=%t attribution=%#v", report.Ready, report.Attribution)
	}
	entry := report.Entries[1]
	if entry.DTBMatches == nil || *entry.DTBMatches || entry.BootDTBSHA256 == entry.InstalledDTBSHA256 {
		t.Fatalf("default mismatch evidence = %#v", entry)
	}
	check := findDoctorCheck(t, report, "grub-entry-dtb", doctorTargetABI)
	if check.State != StateFail || !check.Required {
		t.Fatalf("default mismatch check = %#v", check)
	}
}

// TestInspectABIStampedDTBEvidence verifies real-world loose generation names
// are accepted only after same-ABI digest comparison.
func TestInspectABIStampedDTBEvidence(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		bootDTB     string
		wantMatch   *bool
		wantBootSHA bool
	}{
		{name: "matching digest", token: "/boot/dtb-7.2.2-sp11beta2", bootDTB: "target device tree", wantMatch: boolPointer(true), wantBootSHA: true},
		{name: "mismatched digest", token: "/boot/dtb-7.2.2-sp11beta2", bootDTB: "wrong device tree", wantMatch: boolPointer(false), wantBootSHA: true},
		{name: "conflicting patch line", token: "/boot/dtb-7.2.1-sp11beta2", bootDTB: "target device tree"},
		{name: "malformed identity", token: "/boot/dtb-7.2.2-sp11beta2!", bootDTB: "target device tree"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := singleEntryBootDoctorFixture(t, test.token, test.bootDTB)
			report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled"})
			if err != nil {
				t.Fatal(err)
			}
			entry := report.Entries[0]
			if !equalOptionalBool(entry.DTBMatches, test.wantMatch) {
				t.Fatalf("dtb_matches = %v, want %v", entry.DTBMatches, test.wantMatch)
			}
			if (entry.BootDTBSHA256 != "") != test.wantBootSHA {
				t.Fatalf("boot digest = %q, expected digest=%t", entry.BootDTBSHA256, test.wantBootSHA)
			}
			if test.wantBootSHA && entry.BootDTBState != "present" {
				t.Fatalf("boot DTB state = %q", entry.BootDTBState)
			}
			if !test.wantBootSHA && (entry.InstalledDTBSHA256 != "" || entry.InstalledDTBState != "") {
				t.Fatalf("malformed token triggered digest comparison: %#v", entry)
			}
		})
	}
}

// TestInspectPermissionDeniedDTBWarnsWithoutBlocking applies the same
// cannot-verify semantics to required devicetree evidence.
func TestInspectPermissionDeniedDTBWarnsWithoutBlocking(t *testing.T) {
	root := singleEntryBootDoctorFixture(t, "/boot/dtb-7.2.2-sp11beta2", "target device tree")
	path := filepath.Join(root, "boot/dtb-7.2.2-sp11beta2")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(path, 0o600) }()
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("current user can read chmod 0000 files")
	}
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled"})
	if err != nil {
		t.Fatal(err)
	}
	entry := report.Entries[0]
	check := findDoctorCheck(t, report, "grub-entry-dtb", doctorTargetABI)
	if !report.Ready || check.State != StateWarn || !check.Required || !strings.Contains(check.Detail, "sudo") {
		t.Fatalf("permission-only DTB readiness=%t check=%#v", report.Ready, check)
	}
	if entry.BootDTBState != "inaccessible" || entry.DTBMatches != nil {
		t.Fatalf("permission DTB state = %#v", entry)
	}
}

// TestInspectPermissionDeniedArtefactsWarnsWithoutBlocking verifies unreadable
// root-owned-style files remain distinct from absent required boot artefacts.
func TestInspectPermissionDeniedArtefactsWarnsWithoutBlocking(t *testing.T) {
	root := singleEntryBootDoctorFixture(t, "/boot/dtb-7.2.2-sp11beta2", "target device tree")
	paths := []string{
		filepath.Join(root, "boot/vmlinuz-"+doctorTargetABI),
		filepath.Join(root, "boot/initrd.img-"+doctorTargetABI),
	}
	for _, path := range paths {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		defer func(path string) { _ = os.Chmod(path, 0o600) }(path)
	}
	if _, err := os.ReadFile(paths[0]); err == nil {
		t.Skip("current user can read chmod 0000 files")
	}
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled"})
	if err != nil {
		t.Fatal(err)
	}
	entry := report.Entries[0]
	check := findDoctorCheck(t, report, "grub-entry-artefacts", doctorTargetABI)
	if !report.Ready || check.State != StateWarn || !check.Required || !strings.Contains(check.Detail, "sudo") {
		t.Fatalf("permission-only readiness=%t check=%#v", report.Ready, check)
	}
	if entry.KernelState != "inaccessible" || entry.InitramfsState != "inaccessible" || entry.KernelExists || entry.InitramfsExists {
		t.Fatalf("permission states = %#v", entry)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(encoded), `"inaccessible"`) < 2 {
		t.Fatalf("JSON does not expose inaccessible states: %s", encoded)
	}
}

// TestInspectMissingRequiredArtefactStillFailsClosed preserves required-entry
// readiness when a kernel file is genuinely absent.
func TestInspectMissingRequiredArtefactStillFailsClosed(t *testing.T) {
	root := singleEntryBootDoctorFixture(t, "/boot/dtb-7.2.2-sp11beta2", "target device tree")
	if err := os.Remove(filepath.Join(root, "boot/vmlinuz-"+doctorTargetABI)); err != nil {
		t.Fatal(err)
	}
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled"})
	if err != nil {
		t.Fatal(err)
	}
	check := findDoctorCheck(t, report, "grub-entry-artefacts", doctorTargetABI)
	if report.Ready || check.State != StateFail || !check.Required || report.Entries[0].KernelState != "missing" {
		t.Fatalf("missing-file readiness=%t check=%#v entry=%#v", report.Ready, check, report.Entries[0])
	}
}

// TestAddEntryChecksDTBAvailabilityPrecedence keeps genuine absence and
// positive mismatch failures ahead of the permission-only diagnostic warning.
func TestAddEntryChecksDTBAvailabilityPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		boot      install.GRUBPathAvailability
		installed install.GRUBPathAvailability
		matches   *bool
		wantState State
		wantReady bool
		wantSudo  bool
	}{
		{name: "boot missing installed inaccessible", boot: install.GRUBPathMissing, installed: install.GRUBPathInaccessible, wantState: StateFail},
		{name: "boot inaccessible installed missing", boot: install.GRUBPathInaccessible, installed: install.GRUBPathMissing, wantState: StateFail},
		{name: "present evidence without comparison", boot: install.GRUBPathPresent, installed: install.GRUBPathPresent, wantState: StateFail},
		{name: "positive mismatch", boot: install.GRUBPathPresent, installed: install.GRUBPathPresent, matches: boolPointer(false), wantState: StateFail},
		{name: "boot inaccessible only", boot: install.GRUBPathInaccessible, installed: install.GRUBPathPresent, wantState: StateWarn, wantReady: true, wantSudo: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := Report{Ready: true}
			report.addEntryChecks(Entry{
				ABI:               doctorTargetABI,
				DeviceTrees:       []install.GRUBPathToken{{Command: "devicetree", Path: "/boot/test.dtb"}},
				KernelState:       install.GRUBPathPresent,
				InitramfsState:    install.GRUBPathPresent,
				BootDTBState:      test.boot,
				InstalledDTBState: test.installed,
				DTBMatches:        test.matches,
			}, true)
			check := findDoctorCheck(t, report, "grub-entry-dtb", doctorTargetABI)
			if check.State != test.wantState || report.Ready != test.wantReady || strings.Contains(check.Detail, "sudo") != test.wantSudo {
				t.Fatalf("DTB check=%#v ready=%t", check, report.Ready)
			}
		})
	}
}

// TestAggregateGRUBPathAvailability fixes the multi-token safety ordering.
func TestAggregateGRUBPathAvailability(t *testing.T) {
	tests := []struct {
		name   string
		states []install.GRUBPathAvailability
		want   install.GRUBPathAvailability
	}{
		{name: "present with unsafe sibling", states: []install.GRUBPathAvailability{install.GRUBPathPresent, ""}, want: ""},
		{name: "missing with inaccessible", states: []install.GRUBPathAvailability{install.GRUBPathMissing, install.GRUBPathInaccessible}, want: install.GRUBPathMissing},
		{name: "all inaccessible", states: []install.GRUBPathAvailability{install.GRUBPathInaccessible, install.GRUBPathInaccessible}, want: install.GRUBPathInaccessible},
		{name: "present preserves historical behavior", states: []install.GRUBPathAvailability{install.GRUBPathMissing, install.GRUBPathPresent}, want: install.GRUBPathPresent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := aggregateGRUBPathAvailability(test.states); got != test.want {
				t.Fatalf("availability = %q, want %q", got, test.want)
			}
		})
	}
}

// TestGRUBPathsAvailabilityDoesNotHideUnsafeSibling verifies the filesystem
// inspection continues after present evidence and notices an unsafe route.
func TestGRUBPathsAvailabilityDoesNotHideUnsafeSibling(t *testing.T) {
	root := t.TempDir()
	writeBootDoctorFile(t, root, "boot/initrd-present", "initramfs")
	if err := os.Symlink("initrd-present", filepath.Join(root, "boot/initrd-link")); err != nil {
		t.Skipf("create symlink fixture: %v", err)
	}
	got := grubPathsAvailability(context.Background(), root, []install.GRUBPathToken{
		{Command: "initrd", Path: "/boot/initrd-present"},
		{Command: "initrd", Path: "/boot/initrd-link"},
	})
	if got != "" {
		t.Fatalf("availability = %q, want unsafe empty state", got)
	}
}

// TestInspectResolvesNestedGRUBDefaults verifies top-level numeric selections
// never flatten submenu children and hierarchical numeric paths walk depth.
func TestInspectResolvesNestedGRUBDefaults(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		wantIndex  *int
		wantStale  bool
	}{
		{name: "top-level numeric", configured: "0", wantIndex: intPointer(0)},
		{name: "submenu is not a flat numeric entry", configured: "1", wantStale: true},
		{name: "hierarchical child", configured: "1>0", wantIndex: intPointer(1)},
		{name: "unresolvable hierarchy", configured: "1>1", wantStale: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := bootDoctorFixture(t, "newer patch-line device tree")
			writeBootDoctorFile(t, root, "etc/default/grub", "GRUB_DEFAULT="+test.configured+"\n")
			writeBootDoctorFile(t, root, "boot/grub/grub.cfg", nestedBootDoctorGRUB())
			report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled"})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Entries) != 2 || report.Entries[0].Depth != 0 || report.Entries[1].Depth != 1 {
				t.Fatalf("nested GRUB entries = %#v", report.Entries)
			}
			if report.Default.Stale != test.wantStale || !equalOptionalInt(report.Default.EntryIndex, test.wantIndex) {
				t.Fatalf("default selection = %#v, want index %v stale=%t", report.Default, test.wantIndex, test.wantStale)
			}
		})
	}
}

// nestedBootDoctorGRUB renders one top-level entry followed by one submenu
// containing a child entry for hierarchical default-selection tests.
func nestedBootDoctorGRUB() string {
	return "menuentry 'Legacy " + doctorLegacyABI + "' --id legacy-entry {\n" +
		" linux /boot/vmlinuz-" + doctorLegacyABI + "\n" +
		" devicetree /boot/sp11-denali.dtb\n" +
		" initrd /boot/initrd.img-" + doctorLegacyABI + "\n}\n" +
		"submenu 'Advanced options' {\n" +
		" menuentry 'Newer " + doctorTargetABI + "' --id newer-entry {\n" +
		"  linux /boot/vmlinuz-" + doctorTargetABI + "\n" +
		"  devicetree /boot/sp11-denali.dtb\n" +
		"  initrd /boot/initrd.img-" + doctorTargetABI + "\n" +
		" }\n}\n"
}

// intPointer returns a pointer to one expected default entry index.
func intPointer(value int) *int {
	return &value
}

// equalOptionalInt reports whether two optional integer values agree.
func equalOptionalInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// boolPointer returns a pointer to one expected optional boolean.
func boolPointer(value bool) *bool {
	return &value
}

// equalOptionalBool reports whether two optional booleans agree.
func equalOptionalBool(left, right *bool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// TestInspectMarksSharedDigestAttributionAmbiguous verifies multiple canonical
// ABI candidates sharing the boot digest retain readiness but say ambiguous.
func TestInspectMarksSharedDigestAttributionAmbiguous(t *testing.T) {
	root := bootDoctorFixture(t, "newer patch-line device tree")
	writeBootDoctorFile(t, root, "usr/lib/firmware/"+doctorLegacyABI+"/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb", "newer patch-line device tree")
	report, err := New().Inspect(context.Background(), Options{Root: root, Device: "x1e-oled"})
	if err != nil {
		t.Fatal(err)
	}
	check := findDoctorCheck(t, report, "boot-dtb-attribution", "")
	if !report.Ready || check.State != StatePass || !strings.Contains(check.Detail, "ambiguous") {
		t.Fatalf("shared-digest attribution readiness=%t check=%#v", report.Ready, check)
	}
}

// TestInspectRequiresDeviceForAlternateRoot verifies offline evidence is never
// assigned a hardware variant by guesswork.
func TestInspectRequiresDeviceForAlternateRoot(t *testing.T) {
	_, err := New().Inspect(context.Background(), Options{Root: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "--device is required") {
		t.Fatalf("alternate-root device error = %v", err)
	}
}
