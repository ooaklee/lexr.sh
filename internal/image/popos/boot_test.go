package popos

import (
	"strings"
	"testing"
)

// TestGRUBConfigBindsEveryProfileToPopMedia proves all live entries select
// the actual versioned Casper directory, never Ubuntu Concept's boot paths.
func TestGRUBConfigBindsEveryProfileToPopMedia(t *testing.T) {
	layout, err := parseSourceLayout([]byte(sourceGRUBFixture), []byte(sourceDiskInfoFixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, abi := range []string{"7.2.0-jg-0sp11v23-qcom-x1e", "7.3.0-custom-qcom-x1e"} {
		config, err := grubConfig(layout, abi)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateGRUBConfig([]byte(config), layout, abi); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(config, "cutmem") || strings.Contains(config, "qcom_q6v5_pas") {
			t.Fatal("live menu includes an unsupported bootloader command or DSP override")
		}
		for _, argument := range append(strings.Fields(surfaceKernelArguments), "boot=casper", "live-media-path=/"+layout.liveDirectory) {
			if strings.Count(config, argument) != 3 {
				t.Fatalf("not every live entry contains %s", argument)
			}
		}
		for _, corrupted := range []string{
			strings.Replace(config, "live-media-path=/"+layout.liveDirectory, "live-media-path=/casper", 1),
			strings.Replace(config, "arm64.nopauth", "modprobe.blacklist=qcom_q6v5_pas", 1),
			strings.Replace(config, "/sp11/dtb/x1e80100-microsoft-denali-oled.dtb", "/wrong.dtb", 1),
			config + "\nlinux /other/kernel\n",
		} {
			if err := validateGRUBConfig([]byte(corrupted), layout, abi); err == nil {
				t.Fatal("accepted a changed boot contract")
			}
		}
	}
	for _, abi := range []string{"", "../kernel", "kernel\"; reboot", strings.Repeat("a", 128)} {
		if _, err := grubConfig(layout, abi); err == nil {
			t.Fatalf("accepted unsafe kernel ABI %q", abi)
		}
	}
}
