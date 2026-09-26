package fedora

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// TestRescueDTBBindingPreservesKernelIdentity exercises the copied rescue BLS
// shape produced by Fedora's 51-dracut-rescue.install on both boot layouts.
func TestRescueDTBBindingPreservesKernelIdentity(t *testing.T) {
	const abi = "7.2.0-jg-0sp11v23-qcom-x1e"
	const machineID = "0123456789abcdef0123456789abcdef"
	for _, scenario := range []string{"separate boot", "shared root", "middle devicetree", "tuned initrd", "arbitrary initrd", "duplicate tuned initrd", "unrelated kernel", "comparison error", "missing entry", "duplicate linux", "wrong linux", "directory rescue layout", "missing initrd", "unowned kernel", "unowned DTB", "symlink target", "symlink entry", "symlink kernel", "invalid machine ID"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			boot := filepath.Join(root, "boot")
			module := filepath.Join(root, "module")
			for _, directory := range []string{filepath.Join(boot, "loader/entries"), filepath.Join(module, "dtb/qcom")} {
				if err := os.MkdirAll(directory, 0755); err != nil {
					t.Fatal(err)
				}
			}
			prefix := ""
			if scenario == "shared root" {
				prefix = "/boot"
			}
			rescueABI := "0-rescue-" + machineID
			entry := filepath.Join(boot, "loader/entries", machineID+"-0-rescue.conf")
			rescue := filepath.Join(boot, "vmlinuz-"+rescueABI)
			initrd := filepath.Join(boot, "initramfs-"+rescueABI+".img")
			target := filepath.Join(boot, "dtb-"+rescueABI, "qcom/x1e80100-microsoft-denali-oled.dtb")
			contents := "title Fedora\nversion " + rescueABI + "\nlinux " + prefix + "/vmlinuz-" + rescueABI + "\ninitrd " + prefix + "/initramfs-" + rescueABI + ".img\noptions root=UUID=test\ndevicetree " + prefix + "/dtb-" + rescueABI + "/qcom/x1e80100-microsoft-denali-oled.dtb\n"
			if scenario == "duplicate linux" {
				contents += "linux /other-kernel\n"
			}
			if scenario == "wrong linux" {
				contents = strings.Replace(contents, "linux "+prefix+"/vmlinuz-"+rescueABI, "linux /other-kernel", 1)
			}
			if scenario == "directory rescue layout" {
				contents = "title Fedora Rescue\nversion " + abi + "\nlinux /" + machineID + "/0-rescue/linux\ninitrd /" + machineID + "/0-rescue/initrd\n"
			}
			for name, suffix := range map[string]string{"tuned initrd": " $tuned_initrd", "arbitrary initrd": " /unverified.img", "duplicate tuned initrd": " $tuned_initrd $tuned_initrd"} {
				if scenario == name {
					contents = strings.Replace(contents, ".img\n", ".img"+suffix+"\n", 1)
				}
			}
			expectedContents := contents
			if scenario == "middle devicetree" {
				contents = strings.Replace(contents, "options root=UUID=test\n", "", 1) + "options root=UUID=test\n"
			}
			for name, data := range map[string]string{
				entry: contents, rescue: "selected-EFI-bytes", initrd: "rescue-initramfs",
				filepath.Join(module, "vmlinuz-dtbloader.efi"):                       "selected-EFI-bytes",
				filepath.Join(module, "dtb/qcom/x1e80100-microsoft-denali-oled.dtb"): "selected-X1E-DTB-bytes",
			} {
				if err := os.WriteFile(name, []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "unrelated kernel":
				if err := os.WriteFile(rescue, []byte("older-EFI-bytes"), 0644); err != nil {
					t.Fatal(err)
				}
			case "missing entry", "missing initrd":
				path := entry
				if scenario == "missing initrd" {
					path = initrd
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "symlink target", "symlink entry", "symlink kernel":
				path := target
				if scenario == "symlink entry" {
					path = entry
				} else if scenario == "symlink kernel" {
					path = rescue
				}
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(module, "vmlinuz-dtbloader.efi"), path); err != nil {
					t.Fatal(err)
				}
			}
			script := bindRescueDTBScript(abi)
			script = strings.Replace(script, "boot=/boot", "boot=$TEST_BOOT", 1)
			script = strings.Replace(script, "module=/usr/lib/modules/"+abi, "module=$TEST_MODULE", 1)
			// Native lifecycle validation uses GNU chmod; this fixture also runs on macOS.
			script = strings.Replace(script, `chmod --reference="$entry" "$temporary"`, `chmod 0644 "$temporary"`, 1)
			prelude := "grub2-mkrelpath() { printf '%s%s\\n' \"$TEST_PREFIX\" \"${1#$TEST_BOOT}\"; }\nrestorecon() { :; }\nrpm() { case \"$*\" in */dtb/*) printf '%s' \"$TEST_DTB_OWNER\" ;; *) printf '%s' \"$TEST_OWNER\" ;; esac; }\n"
			if scenario == "comparison error" {
				prelude += "cmp() { return 2; }\n"
			}
			owner, dtbOwner, identity := fedoraKernelPackageName, fedoraKernelPackageName, machineID
			if scenario == "unowned kernel" {
				owner = "unrelated-kernel"
			}
			if scenario == "unowned DTB" {
				dtbOwner = "unrelated-device-tree"
			}
			if scenario == "invalid machine ID" {
				identity = "../../escape"
			}
			invoke := func() ([]byte, error) {
				command := exec.Command("bash", "-ceu", prelude+script)
				command.Env = append(os.Environ(), "TEST_BOOT="+boot, "TEST_MODULE="+module, "TEST_PREFIX="+prefix, "TEST_OWNER="+owner, "TEST_DTB_OWNER="+dtbOwner, "KERNEL_INSTALL_MACHINE_ID="+identity)
				return command.CombinedOutput()
			}
			output, err := invoke()
			wantBinding := scenario == "separate boot" || scenario == "shared root" || scenario == "middle devicetree" || scenario == "tuned initrd"
			wantSuccess := wantBinding || scenario == "unrelated kernel" || scenario == "missing entry"
			if (err == nil) != wantSuccess {
				t.Fatalf("error = %v; output = %s", err, output)
			}
			if !wantBinding {
				if scenario != "missing entry" && scenario != "symlink entry" {
					data, err := os.ReadFile(entry)
					if err != nil || string(data) != contents {
						t.Fatalf("rejected or unrelated BLS was changed: %v: %s", err, data)
					}
				}
				if scenario != "symlink target" {
					if _, err := os.Lstat(target); !os.IsNotExist(err) {
						t.Fatalf("rejected or unrelated entry created a DTB: %v", err)
					}
				}
				return
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "selected-X1E-DTB-bytes" {
				t.Fatalf("wrong rescue DTB: %v: %s", err, data)
			}
			before, err := os.ReadFile(entry)
			if err != nil || string(before) != expectedContents {
				t.Fatalf("rescue BLS changed unrelated fields: %v: %s", err, before)
			}
			// grubby may annotate the rescue entry before a later kernel-install.
			// The literal placeholder must survive the repeated DTB binding.
			before = []byte(strings.Replace(string(before), ".img\n", ".img $tuned_initrd\n", 1))
			if err := os.WriteFile(entry, before, 0644); err != nil {
				t.Fatal(err)
			}
			if output, err := invoke(); err != nil {
				t.Fatalf("repeat binding failed: %v: %s", err, output)
			}
			after, err := os.ReadFile(entry)
			if err != nil || string(before) != string(after) {
				t.Fatalf("repeat binding changed BLS: %v: %s", err, after)
			}
			if err := os.RemoveAll(module); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("removing the ordinary ABI removed rescue DTB: %v", err)
			}
		})
	}
}

// TestRescueDTBHookGatesTheSelectedExternalABI checks the actual hook dispatch.
func TestRescueDTBHookGatesTheSelectedExternalABI(t *testing.T) {
	const abi = "7.2.0-jg-0sp11v23-qcom-x1e"
	for _, delivery := range []kernel.DTBDelivery{kernel.DTBDeliveryEmbedded, kernel.DTBDeliveryExternalRequired} {
		for _, request := range [][2]string{{"add", abi}, {"remove", abi}, {"add", "older-kernel"}} {
			script := strings.Replace(rescueDTBHook(kernel.Bundle{ABI: abi, EffectiveDTBDelivery: delivery}), "exec /usr/lib/lexr/sp11/bind-rescue-dtb", "printf invoked", 1)
			output, err := exec.Command("bash", "-ceu", script, "hook", request[0], request[1]).CombinedOutput()
			wantInvocation := delivery == kernel.DTBDeliveryExternalRequired && request == [2]string{"add", abi}
			if err != nil || (string(output) == "invoked") != wantInvocation {
				t.Fatalf("%s %v: %v: %s", delivery, request, err, output)
			}
		}
	}
}
