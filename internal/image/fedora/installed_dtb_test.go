package fedora

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// TestExternalDTBEntriesKeepCustomAndStockPoliciesSeparate checks every kernel command.
func TestExternalDTBEntriesKeepCustomAndStockPoliciesSeparate(t *testing.T) {
	abi := "7.2.0-jg-0sp11v23-qcom-x1e"
	for _, delivery := range []kernel.DTBDelivery{kernel.DTBDeliveryEmbedded, kernel.DTBDeliveryExternalRequired} {
		config := grubConfig(abi, delivery, fedoraLayoutFixture())
		custom, stock := 0, 0
		lines := strings.Split(config, "\n")
		for index, line := range lines {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "linux ") {
				continue
			}
			if strings.Contains(line, "/loader/linux-fedora ") {
				stock++
				if !strings.Contains(line, "$stock_args") {
					t.Fatal("stock kernel lost its documented DSP workaround")
				}
				continue
			}
			custom++
			if strings.Contains(line, "$stock_args") || strings.Contains(line, "qcom_q6v5_pas") {
				t.Fatal("custom kernel carries the stock DSP blacklist")
			}
			hasDTB := strings.TrimSpace(lines[index+1]) == "devicetree ($root)/sp11/dtb/x1e80100-microsoft-denali-oled.dtb"
			if hasDTB != (delivery == kernel.DTBDeliveryExternalRequired) {
				t.Fatalf("custom DTB binding disagrees with %s", delivery)
			}
		}
		if custom != 4 || stock != 2 {
			t.Fatalf("custom/stock entries = %d/%d", custom, stock)
		}
		if strings.Contains(config, "%!") {
			t.Fatal("GRUB contains an unresolved format operand")
		}
	}
}

// TestInstalledDTBBindingUsesTheActualBootFilesystem exercises exact BLS replacement.
func TestInstalledDTBBindingUsesTheActualBootFilesystem(t *testing.T) {
	const abi = "7.2.0-jg-0sp11v23-qcom-x1e"
	for _, scenario := range []string{"separate boot", "shared root", "ambiguous entry", "missing entry"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			boot := filepath.Join(root, "boot")
			directory := filepath.Join(boot, "loader/entries")
			if err := os.MkdirAll(directory, 0755); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "source.dtb")
			if err := os.WriteFile(source, []byte("exact-X1E-device-tree"), 0644); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(directory, "machine-"+abi+".conf")
			contents := "title Fedora\nversion " + abi + "\nlinux /vmlinuz-" + abi + "\ninitrd /initramfs-" + abi + ".img\noptions root=UUID=test\ndevicetree /stale.dtb\n"
			if scenario != "missing entry" {
				if err := os.WriteFile(entry, []byte(contents), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "ambiguous entry" {
				if err := os.WriteFile(filepath.Join(directory, "other-"+abi+".conf"), []byte(contents), 0644); err != nil {
					t.Fatal(err)
				}
			}
			relative := "/dtb-" + abi + "/qcom/x1e80100-microsoft-denali-oled.dtb"
			if scenario == "shared root" {
				relative = "/boot" + relative
			}
			script := bindInstalledDTBScript(abi)
			script = strings.Replace(script, "boot=/boot", "boot=$TEST_BOOT", 1)
			script = strings.Replace(script, "source=/usr/lib/modules/"+abi+"/dtb/qcom/x1e80100-microsoft-denali-oled.dtb", "source=$TEST_SOURCE", 1)
			// GNU chmod --reference is exercised by native Fedora validation;
			// this host fixture keeps its known 0644 mode on macOS as well.
			script = strings.Replace(script, `chmod --reference="$entry" "$temporary"`, `chmod 0644 "$temporary"`, 1)
			prelude := "grub2-mkrelpath() { printf '%s\\n' \"$TEST_RELATIVE\"; }\nrestorecon() { :; }\n"
			command := exec.Command("bash", "-ceu", prelude+script, "bind-dtb", abi)
			command.Env = append(os.Environ(), "TEST_BOOT="+boot, "TEST_SOURCE="+source, "TEST_RELATIVE="+relative)
			output, err := command.CombinedOutput()
			wantSuccess := scenario == "separate boot" || scenario == "shared root"
			if (err == nil) != wantSuccess {
				t.Fatalf("error = %v; output = %s", err, output)
			}
			if wantSuccess {
				data, err := os.ReadFile(entry)
				if err != nil || strings.Count(string(data), "devicetree ") != 1 || !strings.Contains(string(data), "devicetree "+relative+"\n") || strings.Contains(string(data), "stale.dtb") {
					t.Fatalf("DTB binding was not replaced correctly: %v: %s", err, data)
				}
			}
		})
	}
}

// TestExternalProfileCannotSelectX1PInstallation rejects ambiguous or unqualified profiles.
func TestExternalProfileCannotSelectX1PInstallation(t *testing.T) {
	for _, names := range [][]string{nil, {"surface-pro-11-x1p-lcd"}, {"surface-pro-11-x1e-oled", "surface-pro-11-x1p-lcd"}, {"surface-pro-11-x1e-oled"}} {
		bundle := kernel.Bundle{EffectiveDTBDelivery: kernel.DTBDeliveryExternalRequired}
		for _, name := range names {
			bundle.DeviceTrees = append(bundle.DeviceTrees, kernel.DeviceTree{Device: name, Required: true})
		}
		err := validateExternalProfile(bundle)
		if (err == nil) != (len(names) == 1 && names[0] == "surface-pro-11-x1e-oled") {
			t.Fatalf("profiles %v: error = %v", names, err)
		}
	}
}
