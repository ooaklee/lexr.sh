package elementary

import (
	"strings"
	"testing"
)

// elementarySourceMenu reproduces the two selectors in the inspected source.
const elementarySourceMenu = `menuentry "Try or install elementary OS" {
 linux /casper/vmlinuz boot=casper maybe-ubiquity quiet splash
 initrd /casper/initrd.lz
}
menuentry "Safe graphics" {
 linux /casper/vmlinuz boot=casper maybe-ubiquity quiet splash nomodeset
 initrd /casper/initrd.lz
}
`

// TestSourceLayoutRejectsOtherMedia prevents Debian-family similarity from
// accepting another distro or a kernel separate from the installer's payload.
func TestSourceLayoutRejectsOtherMedia(t *testing.T) {
	info := []byte(`elementary OS 8.1 "circe" - stable arm64 (20260219)`)
	layout, err := parseSourceLayout([]byte(elementarySourceMenu), info)
	if err != nil || layout.kernel != "casper/vmlinuz" || layout.initrd != "casper/initrd.lz" {
		t.Fatalf("layout=%+v error=%v", layout, err)
	}
	for _, tt := range []struct{ name, menu, identity string }{
		{"pop source", elementarySourceMenu, `Pop_OS 24.04 - Release arm64 (20260101)`},
		{"amd64", elementarySourceMenu, strings.Replace(string(info), "arm64", "amd64", 1)},
		{"different kernel", strings.Replace(elementarySourceMenu, "/casper/vmlinuz", "/live/vmlinuz", 1), string(info)},
		{"different initrd", strings.Replace(elementarySourceMenu, "/casper/initrd.lz", "/casper/initrd.gz", 1), string(info)},
		{"wrong live root", strings.Replace(elementarySourceMenu, "boot=casper", "boot=casper live-media-path=/elsewhere", 1), string(info)},
		{"multiple boot selectors", strings.Replace(elementarySourceMenu, "boot=casper", "boot=casper boot=casper", 1), string(info)},
		{"incomplete pairing", strings.Replace(elementarySourceMenu, " initrd /casper/initrd.lz", "", 1), string(info)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseSourceLayout([]byte(tt.menu), []byte(tt.identity)); err == nil {
				t.Fatal("accepted mismatched source")
			}
		})
	}
}

// TestLiveGRUBKeepsCasperAndDeviceTrees checks normal and text boot contracts
// and guards against carrying the earlier Pop diagnostic breakpoint into release media.
func TestLiveGRUBKeepsCasperAndDeviceTrees(t *testing.T) {
	layout := sourceLayout{"casper", "casper/vmlinuz", "casper/initrd.lz"}
	config, err := grubConfig(layout, "7.2.0-jg-0sp11v23-qcom-x1e")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"insmod all_video", "insmod gfxterm", "terminal_output gfxterm", "debug=vc", "devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb", "devicetree /sp11/dtb/x1p64100-microsoft-denali.dtb"} {
		if !strings.Contains(config, required) {
			t.Errorf("missing %q", required)
		}
	}
	for _, forbidden := range []string{"break=", "blacklist=qcom_q6v5_pas", "kernelstub", "systemd-boot", "initrd.gz", "vmlinuz.efi", "ignore_loglevel", "maybe-ubiquity"} {
		if strings.Contains(config, forbidden) {
			t.Errorf("unexpected %q", forbidden)
		}
	}
	if strings.Count(config, "linux /casper/vmlinuz boot=casper live-media-path=/casper") != 4 || strings.Count(config, "initrd /casper/initrd.lz") != 4 {
		t.Fatal("unpaired live entries")
	}
	// The fallback changes graphics only for its labelled diagnostic entry.
	// Normal desktop boot must retain msm, and every entry retains DSP/USB.
	for _, entry := range strings.Split(config, "menuentry ")[1:] {
		fallback := strings.HasPrefix(entry, `"elementary OS for Surface Pro 11 X1E/OLED (firmware display diagnostics)"`)
		if strings.Contains(entry, "module_blacklist=msm") != fallback {
			t.Fatal("graphics blacklist escaped its diagnostic entry")
		}
		if fallback && (!strings.Contains(entry, "plymouth.enable=0") || !strings.Contains(entry, "systemd.unit=multi-user.target")) {
			t.Fatal("firmware display diagnostic attempts graphical startup")
		}
	}
	if err := validateGRUBConfig([]byte(config+"linux /other"), layout, "7.2.0-jg-0sp11v23-qcom-x1e"); err == nil {
		t.Fatal("accepted an added boot command")
	}
}
