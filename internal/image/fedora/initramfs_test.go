package fedora

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/image/sp11"
)

// Run the production archive checker with an unpacked fixture and data-only
// kmod/lsinitrd substitutes. Every required driver has independent lookup data
// and bytes, so an ABI directory or a root-only lookup cannot hide omissions.
// Real kmod/archive qualification remains a separate container integration.
func TestInitramfsValidatorRequiresEarlyPayloadBytes(t *testing.T) {
	for _, testcase := range []struct {
		name, missing, replace, value, failure string
	}{
		{name: "valid"},
		{name: "built-in keyboard parents"},
		{name: "missing UART", missing: "qcom_geni_serial", failure: "module is missing"},
		{name: "missing registry", missing: "surface_aggregator_registry", failure: "module is missing"},
		{name: "missing UCSI", missing: "ucsi_glink", failure: "module is missing"},
		{name: "missing UCSI protocol", missing: "typec_ucsi", failure: "module is missing"},
		{name: "missing retimer", missing: "ps883x", failure: "module is missing"},
		{name: "missing storage", missing: "usb_storage", failure: "module is missing"},
		{name: "missing PCI module", missing: "ath12k_pci", failure: "module is missing"},
		{name: "altered module", replace: "qrtr", value: "stale-module", failure: "module bytes differ"},
		{name: "missing GPU", missing: "GPU", failure: "firmware is missing"},
		{name: "altered GPU", replace: "GPU", value: "stale-firmware", failure: "firmware bytes differ"},
		{name: "missing Wi-Fi", missing: "Wi-Fi", failure: "firmware is missing"},
		{name: "altered Wi-Fi", replace: "Wi-Fi", value: "wrong-board", failure: "firmware bytes differ"},
		{name: "missing dependency text", missing: "modules.dep", failure: "module metadata is missing"},
		{name: "missing dependency index", missing: "modules.dep.bin", failure: "module metadata is missing"},
		{name: "missing alias text", missing: "modules.alias", failure: "module metadata is missing"},
		{name: "missing alias index", missing: "modules.alias.bin", failure: "module metadata is missing"},
		{name: "empty alias index", replace: "modules.alias.bin", value: "", failure: "module metadata is missing"},
		{name: "missing builtin text", missing: "modules.builtin", failure: "module metadata is missing"},
		{name: "missing builtin index", missing: "modules.builtin.bin", failure: "module metadata is missing"},
		{name: "corrupt dependency lookup", replace: "modules.dep.bin", value: "corrupt-index", failure: "cannot resolve required driver"},
		{name: "outside ABI", replace: "lookup/qrtr", value: "insmod ../other/module.ko", failure: "cannot resolve required driver"},
		{name: "builtin mismatch", replace: "lookup/qcom_pd_mapper", value: "builtin different", failure: "module closure differs"},
		{name: "missing transitive lookup", replace: "lookup/ucsi_glink", value: "insmod kernel/ucsi_glink.ko.zst", failure: "module closure differs"},
		{name: "install action", replace: "lookup/qrtr", value: "install /bin/false", failure: "cannot resolve required driver"},
		{name: "extra module parameters", replace: "lookup/qrtr", value: "insmod kernel/qrtr.ko.zst option=1", failure: "cannot resolve required driver"},
		{name: "empty lookup", replace: "lookup/qrtr", value: "", failure: "cannot resolve required driver"},
		{name: "reordered dependencies", replace: "lookup/ucsi_glink", value: "insmod kernel/ucsi_glink.ko.zst\ninsmod kernel/typec_ucsi.ko.zst"},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			root := t.TempDir()
			fixture := t.TempDir()
			const abi = "7.2.0-jg-0sp11v23-qcom-x1e"
			moduleRoot := "usr/lib/modules/" + abi + "/"
			paths := map[string]string{
				"GPU":   "usr/lib/firmware/" + sp11.LiveGPUFirmware[0] + ".xz",
				"Wi-Fi": "usr/lib/firmware/" + sp11.WiFiBoard + ".xz",
			}
			write := func(base, name, value string) {
				t.Helper()
				path := filepath.Join(base, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(value), 0644); err != nil {
					t.Fatal(err)
				}
			}
			for _, base := range []string{root, fixture} {
				for _, metadata := range []string{"modules.dep", "modules.dep.bin", "modules.alias", "modules.alias.bin", "modules.builtin", "modules.builtin.bin"} {
					paths[metadata] = moduleRoot + metadata
					write(base, paths[metadata], "fixture-index")
				}
				for _, driver := range append(append([]string(nil), earlyDrivers...), "ath12k_pci") {
					paths[driver] = moduleRoot + "kernel/" + driver + ".ko.zst"
					write(base, paths[driver], "exact-module-"+driver)
					paths["lookup/"+driver] = moduleRoot + "lookup/" + driver
					lookup := "insmod kernel/" + driver + ".ko.zst"
					if driver == "qcom_pd_mapper" {
						lookup = "builtin qcom_pd_mapper"
					} else if driver == "ucsi_glink" {
						lookup = "insmod kernel/typec_ucsi.ko.zst\n" + lookup
					}
					write(base, paths["lookup/"+driver], lookup)
				}
				for _, firmware := range earlyFirmware() {
					write(base, "usr/lib/firmware/"+firmware+".xz", "exact-firmware")
				}
				if testcase.name == "built-in keyboard parents" {
					for _, driver := range []string{"qcom_geni_serial", "surface_aggregator_registry"} {
						write(base, paths["lookup/"+driver], "builtin "+driver)
						if err := os.Remove(filepath.Join(base, paths[driver])); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if testcase.missing != "" {
				if err := os.Remove(filepath.Join(fixture, paths[testcase.missing])); err != nil {
					t.Fatal(err)
				}
			}
			if testcase.replace != "" {
				write(fixture, paths[testcase.replace], testcase.value)
			}
			prelude := `chroot() {
    local root=$1
    shift
    case "$1" in
        mktemp) mkdir -p "$root/tmp/unpacked"; echo /tmp/unpacked ;;
        /usr/bin/lsinitrd) echo dmsquash-live ;;
        /usr/bin/bash) cp -R "$FIXTURE/." "$root/tmp/unpacked/" ;;
        *) return 1 ;;
    esac
}
modprobe() {
    local view=$2 kind relative extra
    # The fixture lookup follows the requested root, as real kmod must. Index
    # corruption simulates kmod rejecting unreadable shipped dependency data.
    [ "$(cat "$view/usr/lib/modules/$ABI/modules.dep.bin")" = fixture-index ] || return 1
    [[ " $* " = *' -C /dev/null '* ]] || return 1
    while read -r kind relative extra || [ -n "$kind" ]; do
        [ "$kind" != insmod ] || relative="$view/usr/lib/modules/$ABI/$relative"
        printf '%s %s %s\n' "$kind" "$relative" "$extra"
    done < "$view/usr/lib/modules/$ABI/lookup/${@: -1}"
}
modinfo() { return 0; }
`
			script := prelude + strings.Replace(initramfsValidationScript(), "root=/linux-work/rootfs", "root=$ROOT_FIXTURE", 1)
			command := exec.Command("bash", "-ceu", script, "check-initramfs", abi)
			command.Env = append(os.Environ(), "ROOT_FIXTURE="+root, "FIXTURE="+fixture, "ABI="+abi)
			output, err := command.CombinedOutput()
			valid := testcase.name == "valid" || testcase.name == "built-in keyboard parents" || testcase.name == "reordered dependencies"
			if (err == nil) != valid {
				t.Fatalf("error = %v; output = %s", err, output)
			}
			if err != nil && !strings.Contains(string(output), testcase.failure) {
				t.Fatalf("failure does not identify %q: %s", testcase.failure, output)
			}
		})
	}
}

// TestDracutConfigDoesNotForceOrBlacklistDSP preserves normal device coldplug.
func TestDracutConfigDoesNotForceOrBlacklistDSP(t *testing.T) {
	configuration := dracutConfiguration("7.2.0-jg-0sp11v23-qcom-x1e")
	if strings.Contains(configuration, "force_drivers") || strings.Contains(configuration, "blacklist") {
		t.Fatal("early support must use normal coldplug")
	}
	for _, driver := range []string{"qcom_q6v5_pas", "qrtr", "qrtr_smd", "qcom_pd_mapper", "ucsi_glink", "ps883x", "usb_storage", "panel_samsung_atna33xc20", "panel_edp", "qcom_geni_serial", "surface_aggregator_registry"} {
		if !strings.Contains(configuration, " "+driver+" ") {
			t.Errorf("early support omits %s", driver)
		}
	}
	if output, err := exec.Command("bash", "-n", "-c", configuration).CombinedOutput(); err != nil {
		t.Fatalf("configuration is invalid shell: %v: %s", err, output)
	}
}

// TestDracutConfigLeavesStockKernelPolicyAlone prevents custom-only module
// requirements from breaking later generation of the distribution fallback.
func TestDracutConfigLeavesStockKernelPolicyAlone(t *testing.T) {
	script := "kernel=6.19.10-300.fc44.aarch64\nadd_drivers=\ninstall_items=\n" + dracutConfiguration("7.2.0-jg-0sp11v23-qcom-x1e") + "\ntest -z \"$add_drivers$install_items\"\n"
	if output, err := exec.Command("bash", "-ceu", script).CombinedOutput(); err != nil {
		t.Fatalf("stock config unexpectedly requires Surface files: %v: %s", err, output)
	}
}
