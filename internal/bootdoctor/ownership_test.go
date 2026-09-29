package bootdoctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel/install"
)

// TestDoctorMultibootOwnership proves foreign entries are not hashed against
// host files, counted as local ABIs, or silently selected as verified defaults.
func TestDoctorMultibootOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, foreign             string
		defaultForeign, wantReady bool
		ownership                 install.GRUBOwnership
	}{
		{"Arch same ABI", "search --fs-uuid --set=root foreign-boot\n linux /boot/vmlinuz-" + doctorTargetABI + " root=UUID=foreign-root\n initrd /boot/initramfs-" + doctorTargetABI + ".img", false, true, install.GRUBForeign},
		{"Ubuntu same paths", "search --fs-uuid --set=root foreign-boot\n linux /boot/vmlinuz-" + doctorTargetABI + " root=UUID=foreign-root\n initrd /boot/initrd.img-" + doctorTargetABI + "\n devicetree /boot/dtb-" + doctorTargetABI, false, true, install.GRUBForeign},
		{"foreign default", "search --fs-uuid --set=root foreign-boot\n linux /boot/vmlinuz-" + doctorTargetABI + " root=UUID=foreign-root\n initrd /boot/initrd.img-" + doctorTargetABI, true, false, install.GRUBForeign},
		{"unresolved required ABI", "search --fs-uuid --set=root fixture-root\n linux /boot/vmlinuz-" + doctorTargetABI + "\n initrd /boot/initrd.img-" + doctorTargetABI, false, false, install.GRUBUnresolved},
		{"optional Windows chainloader", "search --fs-uuid --set=root foreign-esp\n chainloader /EFI/Microsoft/Boot/bootmgfw.efi", false, true, install.GRUBUnresolved},
		{"optional BLS loader", "blscfg", false, true, install.GRUBUnresolved},
		{"BLS default", "blscfg", true, false, install.GRUBUnresolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := singleEntryBootDoctorFixture(t, "/boot/dtb-"+doctorTargetABI, "target device tree")
			filename := filepath.Join(root, "boot/grub/grub.cfg")
			local, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			foreign := "menuentry 'Other installation' {\n " + tc.foreign + "\n}\n"
			text := string(local) + foreign
			// Raw write: no synthetic ownership is added to the foreign stanza.
			if err := os.WriteFile(filename, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.defaultForeign {
				writeBootDoctorFile(t, root, "etc/default/grub", "GRUB_DEFAULT=1\n")
			}
			report, err := New().Inspect(doctorContext(), Options{Root: root, Device: "x1e-oled", TargetABI: doctorTargetABI})
			if err != nil || report.Ready != tc.wantReady {
				t.Fatalf("ready=%t checks=%+v error=%v", report.Ready, report.Checks, err)
			}
			entry := report.Entries[1]
			if entry.Ownership != tc.ownership || entry.KernelExists || entry.InitramfsExists || entry.DeviceTreeBoot != nil || entry.BootDTBSHA256 != "" {
				t.Fatalf("foreign evidence certified locally: %+v", entry)
			}
			if tc.defaultForeign && report.Attribution.BootSHA256 != "" {
				t.Fatal("foreign default borrowed local boot DTB attribution")
			}
			after, err := os.ReadFile(filename)
			if err != nil || string(after) != text {
				t.Fatal("doctor changed the menu")
			}
			if tc.ownership == install.GRUBForeign {
				if err := os.WriteFile(filename, []byte(foreign), 0o600); err != nil {
					t.Fatal(err)
				}
				writeBootDoctorFile(t, root, "etc/default/grub", "GRUB_DEFAULT=0\n")
				onlyForeign, err := New().Inspect(doctorContext(), Options{Root: root, Device: "x1e-oled", TargetABI: doctorTargetABI})
				if err != nil || onlyForeign.Ready {
					t.Fatalf("foreign-only required ABI accepted: %+v %v", onlyForeign, err)
				}
				missing := false
				for _, check := range onlyForeign.Checks {
					if check.ID == "required-grub-entry" && strings.Contains(check.Detail, "proven-owned") {
						missing = true
					}
				}
				if !missing {
					t.Fatal("foreign-only entry qualified required ABI presence")
				}
			}
		})
	}
}
