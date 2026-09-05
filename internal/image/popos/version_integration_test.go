package popos

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPopInstalledVersionOrderingUsesDpkg exercises the production subprocess
// against Debian's real comparator. Non-Debian hosts retain fixture coverage;
// Linux CI and the explicit Docker integration run cover the actual command.
func TestPopInstalledVersionOrderingUsesDpkg(t *testing.T) {
	if _, err := exec.LookPath("dpkg"); err != nil {
		t.Skip("dpkg is unavailable on this host")
	}
	helper := filepath.Join(t.TempDir(), "pop_boot_refresh.py")
	if err := os.WriteFile(helper, []byte(popBootRefreshHelper), 0o644); err != nil {
		t.Fatal(err)
	}
	const script = `import importlib.util, sys
spec = importlib.util.spec_from_file_location("pop_boot", sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
for left, right, expected in (
    ("7.2.0-jg-0sp11v10", "7.2.0-jg-0sp11v9", 1),
    ("7.2.0-jg-0sp11v23", "7.2.2-jg-0sp11v1", -1),
    ("7.2.0-jg-0sp11v023", "7.2.0-jg-0sp11v23", 0),
    ("7.2.0-jg-0sp11v23+1", "7.2.0-jg-0sp11v23", 1),
):
    actual = module.dpkg_compare_versions(left, right)
    assert actual == expected, (left, right, expected, actual)
for invalid in ("--help", "../outside", "kernel\noptions", ""):
    try:
        module.dpkg_compare_versions(invalid, "7.2.0")
    except module.Fail:
        pass
    else:
        raise AssertionError("unsafe version accepted")
`
	command := exec.Command(popPython(t), "-c", script, helper)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("real dpkg comparison failed: %v\n%s", err, output)
	}
}
