package bootdoctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		" initrd /boot/initrd.img-" + doctorTargetABI + "\n}\n" +
		"menuentry 'Stale at " + root + " 7.1.9-jg-0sp11v99-qcom-x1e' --id stale-entry {\n" +
		" linux /boot/vmlinuz-7.1.9-jg-0sp11v99-qcom-x1e\n" +
		" initrd /boot/initrd.img-7.1.9-jg-0sp11v99-qcom-x1e\n}\n"
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
