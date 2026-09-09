package fedora

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// anacondaPolicyFixture retains Fedora's preservation keys with comments and
// unrelated installer policy so extending the list cannot silently replace it.
const anacondaPolicyFixture = `[Bootloader]
# Keep this source comment and the distribution policy.
type = DEFAULT
preserved_arguments =
    cio_ignore zfcp.allow_lun_scan
    speakup_synth apic noapic apm ide noht acpi video
    pci nodmraid nompath nomodeset noiswmd fips selinux
    biosdevname ipv6.disable net.ifnames net.ifnames.prefix
    nosmt vga rd.net.dns rd.net.dns-resolve-mode rd.net.dns-backend console
# Comments between continuation lines remain in place.
    clk_ignore_unused pd_ignore_unused arm64.nopauth

[Storage]
# Unrelated installer defaults remain byte-for-byte intact.
ibft = True
`

// TestAnacondaBootArgumentsSurviveInstallerDefaults verifies the real parser
// accepts the extension and that only the required two keys are appended.
func TestAnacondaBootArgumentsSurviveInstallerDefaults(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(strings.ReplaceAll(ending, "\n", "LF"), func(t *testing.T) {
			root := t.TempDir()
			original := strings.ReplaceAll(anacondaPolicyFixture, "\n", ending)
			path := writeAnacondaPolicyFixture(t, root, original)
			if output, err := executeAnacondaPolicy(t, root, "check"); err == nil {
				t.Fatalf("unmodified source unexpectedly preserves the full policy: %s", output)
			}
			if output, err := executeAnacondaPolicy(t, root, "prepare"); err != nil {
				t.Fatalf("prepare: %v: %s", err, output)
			}
			actual, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			addition := "    systemd.tpm2_wait soundwire_qcom.sp11_feedback_active_offset2_zero" + ending
			if strings.Count(string(actual), addition) != 1 || strings.Replace(string(actual), addition, "", 1) != original {
				t.Fatalf("source comments, keys or unrelated policy changed: %s", actual)
			}
			if output, err := executeAnacondaPolicy(t, root, "check"); err != nil {
				t.Fatalf("independent check: %v: %s", err, output)
			}
			if output, err := executeAnacondaPolicy(t, root, "prepare"); err != nil {
				t.Fatalf("idempotent prepare: %v: %s", err, output)
			}
			repeated, _ := os.ReadFile(path)
			if !bytes.Equal(repeated, actual) {
				t.Fatal("repeated preparation changed the policy")
			}
		})
	}
}

// TestAnacondaBootPolicyRejectsAmbiguity keeps unknown source overrides from
// defeating the installed arguments while leaving a failed input untouched.
func TestAnacondaBootPolicyRejectsAmbiguity(t *testing.T) {
	for _, scenario := range []string{"duplicate key", "duplicate section", "missing list", "invalid key", "drop-in override", "profile override", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			original := anacondaPolicyFixture
			switch scenario {
			case "duplicate key":
				original = strings.Replace(original, "type = DEFAULT", "preserved_arguments = quiet", 1)
			case "duplicate section":
				original += "\n[Bootloader]\npreserved_arguments = quiet\n"
			case "missing list":
				original = strings.Replace(original, "preserved_arguments =", "other_arguments =", 1)
			case "invalid key":
				original = strings.Replace(original, "arm64.nopauth", "root=live:CDLABEL=Fedora", 1)
			}
			path := writeAnacondaPolicyFixture(t, root, original)
			if strings.HasSuffix(scenario, "override") {
				directory := "conf.d"
				if scenario == "profile override" {
					directory = "profile.d"
				}
				override := filepath.Join(root, "etc/anaconda", directory)
				if err := os.MkdirAll(override, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(override, "override.conf"), []byte("[Bootloader]\npreserved_arguments = console\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "symlink" {
				if err := os.Rename(path, path+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".saved", path); err != nil {
					t.Fatal(err)
				}
			}
			if output, err := executeAnacondaPolicy(t, root, "prepare"); err == nil {
				t.Fatalf("ambiguous input accepted: %s", output)
			}
			retained, _ := os.ReadFile(path)
			if string(retained) != original {
				t.Fatal("failed preparation changed the source")
			}
		})
	}
}

// writeAnacondaPolicyFixture creates an isolated source policy for parser tests.
func writeAnacondaPolicyFixture(t *testing.T, root, contents string) string {
	t.Helper()
	path := filepath.Join(root, "etc/anaconda/anaconda.conf")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// executeAnacondaPolicy exercises the production script with the host's parser.
func executeAnacondaPolicy(t *testing.T, root, operation string) ([]byte, error) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required for the Anaconda parser fixture")
	}
	arguments := append([]string{"-c", anacondaBootPolicyScript, root, operation}, anacondaBootArgumentKeys()...)
	return exec.Command("python3", arguments...).CombinedOutput()
}
