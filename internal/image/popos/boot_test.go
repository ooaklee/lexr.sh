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
			if strings.Count(config, argument) != 4 {
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

// TestDiagnosticEntriesIsolateEFILoader keeps the kernel inputs identical so
// a physical comparison changes only the EFI image-loading implementation.
func TestDiagnosticEntriesIsolateEFILoader(t *testing.T) {
	layout, err := parseSourceLayout([]byte(sourceGRUBFixture), []byte(sourceDiskInfoFixture))
	if err != nil {
		t.Fatal(err)
	}
	config, err := grubConfig(layout, "7.2.0-jg-0sp11v23-qcom-x1e")
	if err != nil {
		t.Fatal(err)
	}
	entries := strings.Split(config, "menuentry ")
	var baseline, firmware string
	for _, entry := range entries {
		switch {
		case strings.Contains(strings.SplitN(entry, "\n", 2)[0], "(text diagnostics)"):
			baseline = entry
		case strings.Contains(strings.SplitN(entry, "\n", 2)[0], "(firmware loader diagnostics)"):
			firmware = entry
		}
	}
	if baseline == "" || firmware == "" {
		t.Fatal("missing paired EFI diagnostic entries")
	}
	for _, prefix := range []string{"    if ! linux ", "    if ! devicetree ", "    if ! initrd "} {
		var lines []string
		for _, entry := range []string{baseline, firmware} {
			for _, line := range strings.Split(entry, "\n") {
				if strings.HasPrefix(line, prefix) {
					lines = append(lines, line)
				}
			}
		}
		if len(lines) != 2 || lines[0] != lines[1] {
			t.Fatalf("diagnostic entries differ in %s inputs: %v", prefix, lines)
		}
	}
	if strings.Contains(baseline, "rmmod peimage") || strings.Contains(firmware, "insmod peimage") || strings.Count(firmware, "rmmod peimage") != 1 ||
		strings.Index(firmware, "rmmod peimage") > strings.Index(firmware, "if ! linux ") {
		t.Fatal("firmware comparison does not select its loader before loading Linux")
	}
	for _, entry := range []string{baseline, firmware} {
		for _, required := range []string{"earlycon=efifb,ram", "efi=debug", "loglevel=8", "terminal_output console", "set debug=linux,efi,peimage", "[4/4] Starting kernel", "    boot\n    lexr_boot_failed"} {
			if !strings.Contains(entry, required) {
				t.Fatalf("diagnostics omit %s", required)
			}
		}
		for _, forbidden := range []string{" quiet ", " splash ", "efi=noruntime", "memmap="} {
			if strings.Contains(entry, forbidden) {
				t.Fatalf("diagnostic comparison changes another boot variable: %s", forbidden)
			}
		}
	}
	if !strings.Contains(config, "sleep --interruptible 60\n    exit 1") {
		t.Fatal("failed partial boot must leave GRUB instead of reaching its automatic boot")
	}
}
