package popos

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPopESPOverrideStillRequiresDeclaredDevice prevents an explicit path from
// bypassing the fstab-to-mounted-device identity check.
func TestPopESPOverrideStillRequiresDeclaredDevice(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		fixture := writePopFixture(t, true)
		arguments := []string{"refresh", "--root", fixture.root, "--abi", popInstalledTestABI, "--image", "/boot/vmlinuz-" + popInstalledTestABI}
		if explicit {
			arguments = append(arguments, "--esp", fixture.esp)
		}
		output, status := runPopHelperMismatchedESP(t, fixture.root, arguments...)
		if status != 65 {
			t.Fatalf("explicit=%t: mismatched device accepted: %d %s", explicit, status, output)
		}
		if _, err := os.Stat(filepath.Join(fixture.esp, "EFI/lexr")); !os.IsNotExist(err) {
			t.Fatal("wrong ESP received a payload")
		}
	}
}

// TestPopRemovalRejectsMissingEntryAndEscapingReceipt checks that corruption
// cannot turn a receipt into a deletion path or partially delete an old kernel.
func TestPopRemovalRejectsMissingEntryAndEscapingReceipt(t *testing.T) {
	for _, corruption := range []string{"missing entry", "escaping generation", "unknown receipt field"} {
		t.Run(corruption, func(t *testing.T) {
			fixture := writePopFixture(t, true)
			if output, status := popRefresh(t, fixture, popInstalledTestABI); status != 0 {
				t.Fatalf("initial refresh: %d %s", status, output)
			}
			receipt := popReceipt(t, fixture, popInstalledTestABI)
			generation := filepath.Join(fixture.esp, "EFI/lexr", popTestRootUUID, popInstalledTestABI, receipt["generation"])
			if corruption == "missing entry" {
				if err := os.Remove(popEntryPath(fixture, popInstalledTestABI)); err != nil {
					t.Fatal(err)
				}
			} else {
				if corruption == "escaping generation" {
					receipt["generation"] = "../../outside"
				} else {
					receipt["unexpected"] = "field"
				}
				data, err := json.Marshal(receipt)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(fixture.root, "var/lib/lexr/pop-boot", popTestRootUUID, popInstalledTestABI+".json"), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Remove(filepath.Join(fixture.root, "boot/vmlinuz-"+popInstalledTestABI)); err != nil {
				t.Fatal(err)
			}
			output, status := runPopHelper(t, fixture.root, "remove", "--root", fixture.root, "--abi", popInstalledTestABI)
			if status != 65 {
				t.Fatalf("corrupt removal accepted: %d %s", status, output)
			}
			for _, name := range []string{"vmlinuz", "initrd.img", "devicetree.dtb"} {
				if _, err := os.Stat(filepath.Join(generation, name)); err != nil {
					t.Fatalf("preflight removed %s: %v", name, err)
				}
			}
		})
	}
}
