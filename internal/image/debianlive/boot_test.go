package debianlive

import (
	"strings"
	"testing"
)

// TestDebianLiveEntriesRetainDiscoveryAndDeviceTrees checks every generated
// kernel entry, including diagnostics, without relying on a single match.
func TestDebianLiveEntriesRetainDiscoveryAndDeviceTrees(t *testing.T) {
	config, err := grubConfig(outputLayout, "7.2.2-jg-0sp11v3-qcom-x1e")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range strings.Split(config, "menuentry ")[1:] {
		if !strings.Contains(entry, "linux ") {
			continue
		}
		count++
		for _, required := range []string{"linux /live/vmlinuz boot=live components live-media-path=/live", "devicetree /sp11/dtb/", "initrd /live/initrd.img"} {
			if !strings.Contains(entry, required) {
				t.Fatalf("entry lacks %q: %s", required, entry)
			}
		}
		for _, forbidden := range []string{"boot=casper", "findiso=", "install_start.cfg", "qcom_q6v5_pas"} {
			if strings.Contains(entry, forbidden) {
				t.Fatalf("unexpected selector %q: %s", forbidden, entry)
			}
		}
	}
	if count != 6 {
		t.Fatalf("live entries=%d", count)
	}
	if strings.Contains(config, "toram=") {
		t.Fatal("module-only RAM copy loses the installer and companion paths")
	}
	if strings.Count(config, " toram ") != 1 {
		t.Fatal("exactly one entry must opt in to whole-medium RAM copying")
	}
	for _, bad := range []sourceLayout{{"casper", "casper/vmlinuz", "casper/initrd"}, {"live", "live/vmlinuz-6.10.6-arm64", "live/initrd.img-6.10.6-arm64"}} {
		if _, err := grubConfig(bad, "7.2.2-jg-0sp11v3-qcom-x1e"); err == nil {
			t.Fatalf("accepted noncanonical paths: %v", bad)
		}
	}
	if _, err := grubConfig(outputLayout, "7.2; reboot"); err == nil {
		t.Fatal("accepted unsafe ABI")
	}
	if err := validateGRUBConfig([]byte(config+"menuentry 'unqualified installer' {}\n"), outputLayout, "7.2.2-jg-0sp11v3-qcom-x1e"); err == nil {
		t.Fatal("accepted extra boot entry")
	}
}

// TestStorageDiagnosticStopsBeforeRootUserspace verifies that only the explicit
// storage entry requests an initramfs shell, using the firmware display while
// retaining ordinary USB coldplug and the default desktop graphics policy.
func TestStorageDiagnosticStopsBeforeRootUserspace(t *testing.T) {
	config, err := grubConfig(outputLayout, "7.2.0-jg-0sp11v23-qcom-x1e")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range strings.Split(config, "menuentry ")[1:] {
		storage := strings.HasPrefix(entry, `"Debian for Surface Pro 11 X1E/OLED (initramfs storage diagnostics)"`)
		if !storage {
			if strings.Contains(entry, "break=") {
				t.Fatalf("ordinary entry unexpectedly stops in initramfs: %s", entry)
			}
			if !strings.Contains(entry, "(firmware display diagnostics)") && strings.Contains(entry, "module_blacklist=") {
				t.Fatalf("ordinary entry unexpectedly disables a driver: %s", entry)
			}
			continue
		}
		found = true
		var arguments []string
		for _, line := range strings.Split(entry, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "linux ") {
				arguments = strings.Fields(line)
			}
		}
		if len(arguments) == 0 || arguments[len(arguments)-1] != "---" {
			t.Fatal("storage entry lacks a terminated kernel command")
		}
		counts := make(map[string]int)
		for _, argument := range arguments {
			counts[argument]++
			if strings.HasPrefix(argument, "module_blacklist=") && argument != "module_blacklist=msm" {
				t.Fatalf("storage diagnostic disables a driver beyond display: %q", argument)
			}
			for _, forbidden := range []string{"toram", "modprobe.blacklist=", "quiet", "splash", "systemd.unit="} {
				if strings.HasPrefix(argument, forbidden) {
					t.Fatalf("storage diagnostic alters the boot under investigation: %q", argument)
				}
			}
		}
		for _, required := range strings.Fields(surfaceKernelArguments + " break=bottom module_blacklist=msm regulator_ignore_unused debug earlycon=efifb,ram loglevel=7 log_buf_len=8M plymouth.enable=0 console=tty0") {
			if counts[required] != 1 {
				t.Fatalf("storage diagnostic requires one %q, got %d", required, counts[required])
			}
		}
		if strings.Count(entry, "break=") != 1 || !strings.Contains(entry, "devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb") {
			t.Fatal("storage diagnostic has ambiguous breakpoints or the wrong device tree")
		}
	}
	if !found {
		t.Fatal("storage diagnostic entry is absent")
	}
}

// TestFirmwareDiagnosticsPreserveUnusedRegulators confines the global regulator
// cleanup bypass to diagnostics that leave the native display driver unloaded.
func TestFirmwareDiagnosticsPreserveUnusedRegulators(t *testing.T) {
	const abi = "7.2.0-jg-0sp11v23-qcom-x1e"
	config, err := grubConfig(outputLayout, abi)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateGRUBConfig([]byte(config), outputLayout, abi); err != nil {
		t.Fatal(err)
	}
	diagnostics := 0
	for _, entry := range strings.Split(config, "menuentry ")[1:] {
		if !strings.Contains(entry, "linux ") {
			continue
		}
		title, _, _ := strings.Cut(entry, "\n")
		diagnostic := title == `"Debian for Surface Pro 11 X1E/OLED (firmware display diagnostics)" {` ||
			title == `"Debian for Surface Pro 11 X1E/OLED (initramfs storage diagnostics)" {`
		want := 0
		if diagnostic {
			want = 1
			diagnostics++
		}
		counts := make(map[string]int)
		for _, line := range strings.Split(entry, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "linux ") {
				continue
			}
			for _, argument := range strings.Fields(line) {
				counts[argument]++
				if strings.HasPrefix(argument, "regulator_ignore_unused=") {
					t.Fatalf("regulator cleanup bypass must be a bare flag: %s", title)
				}
			}
		}
		for _, argument := range []string{"regulator_ignore_unused", "module_blacklist=msm"} {
			if counts[argument] != want {
				t.Fatalf("%s: want %d %q, got %d", title, want, argument, counts[argument])
			}
		}
		if !diagnostic {
			continue
		}
		t.Run(title, func(t *testing.T) {
			removed := strings.Replace(entry, " regulator_ignore_unused ", " ", 1)
			for name, altered := range map[string]string{
				"removed":          removed,
				"duplicated":       strings.Replace(entry, " regulator_ignore_unused ", " regulator_ignore_unused regulator_ignore_unused ", 1),
				"after separator":  strings.Replace(removed, " ---", " --- regulator_ignore_unused", 1),
				"assigned a value": strings.Replace(entry, " regulator_ignore_unused ", " regulator_ignore_unused=1 ", 1),
			} {
				t.Run(name, func(t *testing.T) {
					mutated := strings.Replace(config, entry, altered, 1)
					if err := validateGRUBConfig([]byte(mutated), outputLayout, abi); err == nil {
						t.Fatal("accepted altered diagnostic regulator policy")
					}
				})
			}
			t.Run("moved to desktop", func(t *testing.T) {
				mutated := strings.Replace(config, entry, removed, 1)
				if !strings.Contains(mutated, " quiet splash ") {
					t.Fatal("desktop fixture lacks the expected kernel argument anchor")
				}
				mutated = strings.Replace(mutated, " quiet splash ", " regulator_ignore_unused quiet splash ", 1)
				if err := validateGRUBConfig([]byte(mutated), outputLayout, abi); err == nil {
					t.Fatal("accepted regulator bypass moved from diagnostics to the desktop")
				}
			})
		})
	}
	if diagnostics != 2 {
		t.Fatalf("want two firmware display diagnostics, got %d", diagnostics)
	}
}
