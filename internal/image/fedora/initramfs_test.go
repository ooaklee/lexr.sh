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
// kmod/lsinitrd substitutes. The filesystem comparisons remain real, so merely
// retaining an ABI directory cannot hide a lost module or stale firmware file.
func TestInitramfsValidatorRequiresEarlyPayloadBytes(t *testing.T) {
	for _, scenario := range []string{"valid", "missing module", "altered module", "missing GPU", "altered GPU", "missing Wi-Fi", "altered Wi-Fi", "missing PCI module", "outside ABI"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			fixture := t.TempDir()
			const abi = "7.2.0-jg-0sp11v23-qcom-x1e"
			module := "usr/lib/modules/" + abi + "/kernel/net/qrtr/qrtr.ko.zst"
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
				write(base, module, "exact-module")
				for _, firmware := range earlyFirmware() {
					write(base, "usr/lib/firmware/"+firmware+".xz", "exact-firmware")
				}
			}
			switch scenario {
			case "missing module":
				_ = os.Remove(filepath.Join(fixture, module))
			case "altered module":
				write(fixture, module, "stale-module")
			case "missing GPU":
				_ = os.Remove(filepath.Join(fixture, "usr/lib/firmware/"+sp11.LiveGPUFirmware[0]+".xz"))
			case "altered GPU":
				write(fixture, "usr/lib/firmware/"+sp11.LiveGPUFirmware[0]+".xz", "stale-firmware")
			case "missing Wi-Fi":
				_ = os.Remove(filepath.Join(fixture, "usr/lib/firmware/"+sp11.WiFiBoard+".xz"))
			case "altered Wi-Fi":
				write(fixture, "usr/lib/firmware/"+sp11.WiFiBoard+".xz", "wrong-board")
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
    if [ "${@: -1}" = ath12k_pci ]; then
        echo "insmod $ROOT_FIXTURE/usr/lib/modules/$ABI/kernel/ath12k_pci.ko"
    elif [ "${@: -1}" = qrtr ]; then
        if [ "$SCENARIO" = 'outside ABI' ]; then
            echo "insmod $ROOT_FIXTURE/usr/lib/modules/other/module.ko"
        else
            echo "insmod $ROOT_FIXTURE/$MODULE"
        fi
    else
        echo "builtin ${@: -1}"
    fi
}
modinfo() { [ "$SCENARIO" = 'missing PCI module' ]; }
`
			script := prelude + strings.Replace(initramfsValidationScript(), "root=/linux-work/rootfs", "root=$ROOT_FIXTURE", 1)
			command := exec.Command("bash", "-ceu", script, "check-initramfs", abi)
			command.Env = append(os.Environ(), "ROOT_FIXTURE="+root, "FIXTURE="+fixture, "MODULE="+module, "SCENARIO="+scenario, "ABI="+abi)
			output, err := command.CombinedOutput()
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("error = %v; output = %s", err, output)
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
	for _, driver := range []string{"qcom_q6v5_pas", "qrtr", "qrtr_smd", "qcom_pd_mapper", "ucsi_glink", "ps883x", "usb_storage", "panel_samsung_atna33xc20", "panel_edp"} {
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
