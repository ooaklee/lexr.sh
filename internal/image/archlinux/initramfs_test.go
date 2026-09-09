package archlinux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEarlySupportHookFailsClosed executes the build hook against mkinitcpio API
// substitutes to verify a missing driver or firmware aborts image generation.
func TestEarlySupportHookFailsClosed(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required to execute mkinitcpio build hooks")
	}
	hook := filepath.Join(t.TempDir(), "lexr_sp11")
	if err := os.WriteFile(hook, []byte(EarlySupportHook()), 0o644); err != nil {
		t.Fatal(err)
	}
	const harness = `source "$1"
add_module() { echo "module $1"; [[ "$1" != "$missing" ]]; }
add_firmware() { echo "firmware $1"; [[ "$1" != "$missing" ]]; }
modinfo() { [[ "$missing" == "ath12k_pci" ]]; }
missing=$2
build
`
	for _, missing := range []string{"", "qrtr_smd", "panel_samsung_atna33xc20", "ath12k", "ath12k_pci", "qcom/gen70500_gmu.bin", "ath12k/WCN7850/hw2.0/board.bin", "ath12k/WCN7850/hw2.0/amss.bin", "ath12k/WCN7850/hw2.0/m3.bin"} {
		output, err := exec.Command(bash, "-c", harness, "lexr-hook-test", hook, missing).CombinedOutput()
		if (err != nil) != (missing != "") {
			t.Errorf("missing=%q exit=%v output=%s", missing, err, output)
		}
		if missing == "" && (!strings.Contains(string(output), "module qrtr_smd") || !strings.Contains(string(output), "firmware ath12k/WCN7850/hw2.0/board.bin")) {
			t.Fatal("hook did not request runtime transport and Wi-Fi board data")
		}
	}
}

// TestInitramfsRuntimeSeparation evaluates the generated shell arrays to prove
// BusyBox live discovery is selected only by the live configuration.
func TestInitramfsRuntimeSeparation(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required to evaluate mkinitcpio configuration")
	}
	for _, tc := range []struct {
		name   string
		config string
		live   bool
	}{
		{"live", LiveInitramfsConfig(), true},
		{"installed", InstalledInitramfsConfig(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "mkinitcpio.conf")
			if err := os.WriteFile(file, []byte(tc.config), 0o644); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(bash, "-c", `source "$1"; test "${#MODULES[@]}" -eq 0 || exit 1; printf '%s\n' "${HOOKS[@]}"`, "lexr-config-test", file).CombinedOutput()
			if err != nil {
				t.Fatalf("configuration sets forced modules or is not shell syntax: %v: %s", err, output)
			}
			hooks := "\n" + string(output)
			if !strings.HasPrefix(hooks, "\nbase\nudev\n") || !strings.Contains(hooks, "\nlexr_sp11\n") {
				t.Fatalf("incorrect early runtime: %s", hooks)
			}
			if strings.Contains(hooks, "\narchiso\n") != tc.live || strings.Contains(hooks, "\nautodetect\n") || strings.Contains(hooks, "\nsystemd\n") {
				t.Fatalf("incorrect media or host policy: %s", hooks)
			}
		})
	}
}

// TestInstalledFirmwareAfterImport checks the evaluated config after a local
// firmware import. Missing files remain optional, unrelated files are excluded,
// and the live config never acquires the private installed-system inputs.
func TestInstalledFirmwareAfterImport(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required to evaluate mkinitcpio configuration")
	}
	root := t.TempDir()
	firmwareRoot := filepath.Join(root, "firmware")
	denali := filepath.Join(firmwareRoot, "qcom/x1e80100/microsoft/Denali")
	if err := os.MkdirAll(denali, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"qcadsp8380.mbn", "unrelated.bin"} {
		if err := os.WriteFile(filepath.Join(denali, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	topology := filepath.Join(firmwareRoot, "qcom/x1e80100/X1E80100-Microsoft-Surface-Pro-11-tplg.bin")
	if err := os.WriteFile(topology, []byte("topology fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, live := range []bool{false, true} {
		config := InstalledInitramfsConfig()
		if live {
			config = LiveInitramfsConfig()
		}
		// Redirect only the absolute firmware directory into this test fixture.
		config = strings.ReplaceAll(config, "/usr/lib/firmware/", firmwareRoot+"/")
		path := filepath.Join(root, "config")
		if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command(bash, "-c", `source "$1"; printf '%s\n' "${FILES[@]}"`, "lexr-config-test", path).CombinedOutput()
		if err != nil {
			t.Fatalf("evaluate firmware config: %v: %s", err, output)
		}
		want := topology + "\n" + filepath.Join(denali, "qcadsp8380.mbn")
		if live {
			want = ""
		}
		if strings.TrimSpace(string(output)) != want {
			t.Fatalf("live=%v firmware=%q, want %q", live, output, want)
		}
	}
}
