package bootdoctor

import (
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel/install"
)

// TestInspectAcceptsEmbeddedMultiDeviceInventory keeps read-only doctor aligned
// with fallback validation for kernels embedding the complete Stubble HWID set.
func TestInspectAcceptsEmbeddedMultiDeviceInventory(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("anchored same-ABI discovery requires Linux or macOS")
	}
	payloads := [][]byte{[]byte("target OLED device tree"), []byte("target LCD device tree")}
	root := embeddedBootDoctorFixture(t, payloads...)
	for index := 0; index < 37; index++ {
		payload := fmt.Sprintf("other board DTB %02d", index)
		payloads = append(payloads, []byte(payload))
		writeBootDoctorFile(t, root, "usr/lib/firmware/"+doctorTargetABI+
			fmt.Sprintf("/device-tree/qcom/other-board-%02d.dtb", index), payload)
	}
	writeBootDoctorFile(t, root, "usr/lib/firmware/"+doctorTargetABI+"/device-tree/qcom/not-embedded.dtb", "not embedded")
	writeBootDoctorFile(t, root, "usr/lib/firmware/"+doctorTargetABI+"/device-tree/qcom/overlay.dtbo", "target OLED device tree")
	writeBootDoctorEmbeddedImage(t, filepath.Join(root, "boot/vmlinuz-"+doctorTargetABI), payloads...)
	writeBootDoctorFile(t, root, "boot/grub/grub.cfg", "menuentry 'Ubuntu "+doctorTargetABI+"' --id main-entry {\n"+
		" linux /boot/vmlinuz-"+doctorTargetABI+"\n initrd /boot/initrd.img-"+doctorTargetABI+"\n}\n"+
		"menuentry 'Ubuntu "+doctorTargetABI+" recovery' --id recovery-entry {\n"+
		" linux /boot/vmlinuz-"+doctorTargetABI+" single\n initrd /boot/initrd.img-"+doctorTargetABI+"\n}\n")
	writeBootDoctorFile(t, root, "etc/default/grub", "GRUB_DEFAULT=saved\n")
	writeBootDoctorFile(t, root, "boot/grub/grubenv", "saved_entry=main-entry\n")
	for _, device := range []string{"x1e-oled", "x1p-lcd"} {
		report, err := New().Inspect(doctorContext(), Options{Root: root, Device: device, TargetABI: doctorTargetABI})
		if err != nil {
			t.Fatal(err)
		}
		if !report.Ready || len(report.Entries) != 2 || report.Entries[0].DeviceTreeBoot == nil ||
			report.Entries[0].DeviceTreeBoot.Mode != install.DeviceTreeBootEmbedded ||
			len(report.Entries[0].DeviceTreeBoot.SHA256s) != 39 {
			t.Fatalf("multi-device doctor report ready=%v, entries=%+v", report.Ready, report.Entries)
		}
		selected := digestText("target OLED device tree")
		if device == "x1p-lcd" {
			selected = digestText("target LCD device tree")
		}
		for _, entry := range report.Entries {
			if entry.BootDTBSHA256 != selected || entry.InstalledDTBSHA256 != selected || entry.DTBMatches == nil || !*entry.DTBMatches {
				t.Fatalf("selected device digest was confused with complete-inventory aggregate: %+v", entry)
			}
		}
		attribution := report.Attribution
		if attribution.AttributedABI != doctorTargetABI || attribution.BootSHA256 != selected || attribution.InstalledSHA256 != selected ||
			attribution.DeviceTreeBoot == nil || len(attribution.DeviceTreeBoot.SHA256s) != 39 ||
			attribution.DeviceTreeBoot.SHA256 == selected || attribution.DeviceTreeBoot.NormalGRUBEntryCount != 1 ||
			attribution.DeviceTreeBoot.RecoveryGRUBEntryCount != 1 {
			t.Fatalf("multi-device default attribution = %+v", attribution)
		}
	}
}

// TestInspectRejectsMultiDeviceImageWithoutSelectedProfile prevents many other
// valid embedded boards from substituting for the requested Surface variant.
func TestInspectRejectsMultiDeviceImageWithoutSelectedProfile(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("anchored same-ABI discovery requires Linux or macOS")
	}
	root := embeddedBootDoctorFixture(t, []byte("target LCD device tree"), []byte("other board"))
	writeBootDoctorFile(t, root, "usr/lib/firmware/"+doctorTargetABI+"/device-tree/qcom/other-board.dtb", "other board")
	report, err := New().Inspect(doctorContext(), Options{Root: root, Device: "x1e-oled", TargetABI: doctorTargetABI})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.Entries[0].DeviceTreeBoot != nil {
		t.Fatalf("other boards satisfied missing selected profile: %+v", report)
	}
}

// TestABIBindingRetainsCompleteDigestSet rejects distinct inventories even if a
// fabricated entry claims the same aggregate string for both.
func TestABIBindingRetainsCompleteDigestSet(t *testing.T) {
	first := install.DeviceTreeBootEvidence{Mode: install.DeviceTreeBootEmbedded, SHA256: "aggregate", SHA256s: []string{"first"}}
	second := install.DeviceTreeBootEvidence{Mode: install.DeviceTreeBootEmbedded, SHA256: "aggregate", SHA256s: []string{"second"}}
	report := Report{Entries: []Entry{
		{ABI: doctorTargetABI, Ownership: install.GRUBOwned, DeviceTreeBoot: &first},
		{ABI: doctorTargetABI, Ownership: install.GRUBOwned, DeviceTreeBoot: &second},
	}}
	if _, valid := report.abiDeviceTreeBinding(doctorTargetABI); valid {
		t.Fatal("different complete embedded inventories were merged")
	}
}
