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
		for _, forbidden := range []string{"boot=casper", "findiso=", "break=", "install_start.cfg", "qcom_q6v5_pas"} {
			if strings.Contains(entry, forbidden) {
				t.Fatalf("unexpected selector %q: %s", forbidden, entry)
			}
		}
	}
	if count != 4 {
		t.Fatalf("live entries=%d", count)
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
