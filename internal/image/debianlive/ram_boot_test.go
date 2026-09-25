package debianlive

import (
	"context"
	"crypto/md5" //nolint:gosec // Exercises Debian's media-check inventory.
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ramBootFixture provides real files and checksum reads, with synthetic kernel
// observations so negative cases do not require privileged host mounts.
type ramBootFixture struct {
	root, medium, mounts, blocks, checksums, cmdline string
}

// newRAMBootFixture creates a complete copied medium with an intentionally
// omitted .disk directory, matching the pinned stock whole-medium copier.
func newRAMBootFixture(t *testing.T) ramBootFixture {
	t.Helper()
	if _, err := exec.LookPath("md5sum"); err != nil {
		t.Skip("GNU md5sum is required to exercise Debian's checksum checker")
	}
	root := t.TempDir()
	f := ramBootFixture{root: root, medium: filepath.Join(root, "medium"), mounts: filepath.Join(root, "mounts"), blocks: filepath.Join(root, "blocks"), checksums: filepath.Join(root, "checksums"), cmdline: filepath.Join(root, "cmdline")}
	var inventory strings.Builder
	for _, member := range []string{"live/filesystem.squashfs", "sp11/lexr-manifest.json", "sp11/companion/lexr", "pool/package.deb"} {
		data := []byte("complete contents of " + member)
		writeRAMFixture(t, filepath.Join(f.medium, member), string(data))
		fmt.Fprintf(&inventory, "%x  ./%s\n", md5.Sum(data), member)
	}
	fmt.Fprintf(&inventory, "%x  ./.disk/info\n", md5.Sum([]byte("omitted by stock copier")))
	writeRAMFixture(t, filepath.Join(f.medium, "md5sum.txt"), inventory.String())
	writeRAMFixture(t, f.mounts, "tmpfs "+f.medium+" tmpfs ro 0 0\n/dev/loop3 /run/live/rootfs/filesystem.squashfs squashfs ro 0 0\n")
	writeRAMFixture(t, filepath.Join(f.blocks, "loop3/loop/backing_file"), f.medium+"/live/filesystem.squashfs\n")
	writeRAMFixture(t, f.cmdline, "boot=live toram console=tty0\n")
	return f
}

// writeRAMFixture writes one private fixture observation or copied member.
func writeRAMFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runRAMCheck executes the shipped shell functions and real md5sum, rather than
// matching implementation strings or mocking the integrity calculation.
func runRAMCheck(t *testing.T, f ramBootFixture, script string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "sh", "-c", ramBootChecks+ramBootWait+script, "lexr-ram-test", f.cmdline, f.medium, f.mounts, f.blocks, f.checksums).CombinedOutput()
}

// TestRAMBootChecksRejectFailedCopies distinguishes verified RAM media from
// native-copy failures, damaged payloads, stale USB backing and invalid lists.
func TestRAMBootChecksRejectFailedCopies(t *testing.T) {
	for _, testcase := range []struct {
		name   string
		mutate func(*testing.T, ramBootFixture)
		valid  bool
	}{
		{"whole medium", nil, true},
		{"USB fallback", func(t *testing.T, f ramBootFixture) {
			writeRAMFixture(t, f.mounts, "/dev/sdb1 "+f.medium+" iso9660 ro 0 0\n/dev/loop3 /run/live/rootfs/filesystem.squashfs squashfs ro 0 0\n")
		}, false},
		{"stale loop backing", func(t *testing.T, f ramBootFixture) {
			writeRAMFixture(t, filepath.Join(f.blocks, "loop3/loop/backing_file"), "/old-usb/live/filesystem.squashfs\n")
		}, false},
		{"nested USB mount", func(t *testing.T, f ramBootFixture) {
			data, _ := os.ReadFile(f.mounts)
			writeRAMFixture(t, f.mounts, string(data)+"/dev/sdb1 "+f.medium+"/live iso9660 ro 0 0\n")
		}, false},
		{"damaged copied filesystem", func(t *testing.T, f ramBootFixture) {
			writeRAMFixture(t, filepath.Join(f.medium, "live/filesystem.squashfs"), "truncated")
		}, false},
		{"copy failed after rootfs", func(t *testing.T, f ramBootFixture) {
			// A real failed copy leaves the rootfs intact but omits the companion.
			// The old helper ignores this exit status and can still mount rootfs.
			if err := os.RemoveAll(filepath.Join(f.medium, "sp11/companion")); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(f.root, "source-lexr")
			writeRAMFixture(t, source, "companion bytes")
			if err := exec.Command("cp", source, filepath.Join(f.medium, "sp11/companion/lexr")).Run(); err == nil {
				t.Fatal("fixture did not reproduce a failed copy")
			}
		}, false},
		{"missing inventory", func(t *testing.T, f ramBootFixture) {
			if err := os.Remove(filepath.Join(f.medium, "md5sum.txt")); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"empty inventory", func(t *testing.T, f ramBootFixture) {
			writeRAMFixture(t, filepath.Join(f.medium, "md5sum.txt"), "")
		}, false},
		{"missing root inventory", func(t *testing.T, f ramBootFixture) {
			path := filepath.Join(f.medium, "md5sum.txt")
			data, _ := os.ReadFile(path)
			writeRAMFixture(t, path, strings.ReplaceAll(string(data), "./live/filesystem.squashfs", "./pool/package.deb"))
		}, false},
		{"duplicate inventory", func(t *testing.T, f ramBootFixture) {
			path := filepath.Join(f.medium, "md5sum.txt")
			data, _ := os.ReadFile(path)
			writeRAMFixture(t, path, string(data)+string(data))
		}, false},
		{"inventory traversal", func(t *testing.T, f ramBootFixture) {
			path := filepath.Join(f.medium, "md5sum.txt")
			data, _ := os.ReadFile(path)
			writeRAMFixture(t, path, string(data)+strings.Repeat("0", 32)+"  ./.disk/../live/outside\n")
		}, false},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			fixture := newRAMBootFixture(t)
			if testcase.mutate != nil {
				testcase.mutate(t, fixture)
			}
			output, err := runRAMCheck(t, fixture, "shift; lexr_verify_ram \"$@\"\n")
			if (err == nil) != testcase.valid {
				t.Fatalf("valid=%v, error=%v, output=%s", testcase.valid, err, output)
			}
		})
	}
}

// TestRAMBootCannotContinueAfterShellExit proves returning from panic retries
// verification, and ordinary USB boots are not subject to the RAM requirement.
func TestRAMBootCannotContinueAfterShellExit(t *testing.T) {
	f := newRAMBootFixture(t)
	writeRAMFixture(t, filepath.Join(f.medium, "live/filesystem.squashfs"), "partial")
	output, err := runRAMCheck(t, f, `attempts=0
panic() {
    attempts=$((attempts + 1))
    [ "$attempts" -lt 2 ] || exit 73
}

lexr_ram_boot "$@"
echo UNSAFE_CONTINUATION
`)
	var exitCode int
	if exit, ok := err.(*exec.ExitError); ok {
		exitCode = exit.ExitCode()
	}
	if exitCode != 73 || strings.Contains(string(output), "UNSAFE_CONTINUATION") {
		t.Fatalf("shell exit bypassed verification: %v\n%s", err, output)
	}
	for _, cmdline := range []string{"boot=live console=tty0", "boot=live nottoram"} {
		writeRAMFixture(t, f.cmdline, cmdline)
		if output, err := runRAMCheck(t, f, `panic() { exit 73; }; lexr_ram_boot "$@"`); err != nil {
			t.Fatalf("ordinary boot was blocked: %v\n%s", err, output)
		}
	}
	for _, cmdline := range []string{"boot=live toram=filesystem.squashfs", "boot=live toram=filesystem.squashfs toram", "boot=live toram toram=filesystem.squashfs"} {
		writeRAMFixture(t, f.cmdline, cmdline)
		output, err := runRAMCheck(t, f, `panic() { exit 73; }; lexr_ram_boot "$@"`)
		if err == nil || !strings.Contains(string(output), "use bare toram") {
			t.Fatalf("module-only RAM copy was accepted: %v\n%s", err, output)
		}
	}
}

// TestRAMBootMountedCopyIntegration checks real tmpfs and loop observations,
// then exercises the old copier's ignored ENOSPC result inside a disposable
// Linux container. It does not mount or expose any host filesystem or device.
func TestRAMBootMountedCopyIntegration(t *testing.T) {
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 for mounted RAM-copy integration")
	}
	image := os.Getenv("LEXR_TEST_TOOLS_IMAGE")
	if image == "" {
		t.Skip("set LEXR_TEST_TOOLS_IMAGE to the prepared Lexr tools image")
	}
	script := ramBootChecks + `
mkdir -p /tmp/source/live /tmp/source/sp11/companion /tmp/source/.disk /tmp/root/etc
printf 'test live root\n' > /tmp/root/etc/issue
mksquashfs /tmp/root /tmp/source/live/filesystem.squashfs -noappend -no-progress -processors 1
printf 'test manifest\n' > /tmp/source/sp11/lexr-manifest.json
printf 'test companion\n' > /tmp/source/sp11/companion/lexr
printf 'hidden media identity\n' > /tmp/source/.disk/info
(cd /tmp/source && md5sum ./live/filesystem.squashfs ./sp11/lexr-manifest.json ./sp11/companion/lexr ./.disk/info) > /tmp/source/md5sum.txt
mkdir -p /run/live/medium /run/live/rootfs/filesystem.squashfs
mount -t tmpfs -o size=2m tmpfs /run/live/medium
trap 'umount /run/live/rootfs/filesystem.squashfs 2>/dev/null || true; umount /run/live/medium 2>/dev/null || true' EXIT
cp -a /tmp/source/* /run/live/medium/
mount -t squashfs -o ro,loop /run/live/medium/live/filesystem.squashfs /run/live/rootfs/filesystem.squashfs
lexr_verify_ram /run/live/medium /proc/mounts /sys/class/block /run/lexr-ram-checksums

# The real filesystem mounted successfully, but a later copy can still fail.
# Ignore that failure as the pinned live-boot helper does, and require the
# guard to reject its readable but incomplete tmpfs medium.
dd if=/dev/zero of=/tmp/source/sp11/companion/large bs=1048576 count=3 status=none
(cd /tmp/source && md5sum ./sp11/companion/large) >> /run/live/medium/md5sum.txt
if cp /tmp/source/sp11/companion/large /run/live/medium/sp11/companion/large; then
    echo 'fixture failed to exhaust the copy filesystem' >&2
    exit 1
fi
if lexr_verify_ram /run/live/medium /proc/mounts /sys/class/block /run/lexr-ram-checksums; then
    echo 'incomplete RAM copy was accepted' >&2
    exit 1
fi
`
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "run", "--rm", "-i", "--network", "none", "--privileged", image, "bash", "-seu")
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("mounted RAM-copy verification: %v\n%s", err, output)
	}
}
