package fedora

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestFedoraUserSupportRejectsChangedSource proves retention never reaches
// Docker when the staged companion violates the existing closed inventory.
func TestFedoraUserSupportRejectsChangedSource(t *testing.T) {
	for _, mutation := range []string{"extra", "changed", "link"} {
		t.Run(mutation, func(t *testing.T) {
			workspace := t.TempDir()
			manifest := fedoraUserSupportFixture(t, workspace, true)
			root := filepath.Join(workspace, "sp11/companion")
			file := filepath.Join(root, "catalogues/supported-isos.json")
			switch mutation {
			case "extra":
				writeUserSupportFixture(t, filepath.Join(root, "extra"), []byte("extra"), 0o644)
			case "changed":
				writeUserSupportFixture(t, file, []byte("changed"), 0o644)
			case "link":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("supported-userspace.json", file); err != nil {
					t.Fatal(err)
				}
			}
			// A nil Docker boundary would panic if source verification were bypassed.
			err := installFedoraUserSupport(context.Background(), nil, "unused", workspace, "unused", manifest)
			if err == nil || !strings.Contains(err.Error(), "validate companion before") {
				t.Fatalf("changed source accepted or reached Docker: %v", err)
			}
		})
	}
}

// TestFedoraGuideUsesSourcePolicy keeps recovery instructions tied to the
// accepted volume identity when a future Fedora release is qualified.
func TestFedoraGuideUsesSourcePolicy(t *testing.T) {
	if strings.Count(fedoraGettingStartedTemplate, "@FEDORA_VOLUME_ID@") != 2 || strings.Contains(fedoraGettingStarted, "@FEDORA_VOLUME_ID@") || strings.Count(fedoraGettingStarted, SourceVolumeID) != 2 {
		t.Fatal("guide discovery drifted from the Fedora source policy")
	}
}

// TestFedoraManifestRetentionRequiresIdenticalEncoding rejects even a
// semantically equivalent ISO inventory that differs from the retained bytes.
func TestFedoraManifestRetentionRequiresIdenticalEncoding(t *testing.T) {
	manifest := completeFedoraManifestFixture()
	data, err := serialiseManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFedoraManifestEncoding(manifest, data); err != nil {
		t.Fatal(err)
	}
	if err := validateFedoraManifestEncoding(manifest, append(data, '\n')); err == nil {
		t.Fatal("different ISO and retained manifest encodings accepted")
	}
}

// TestFedoraGuideCommandsHaveShellSyntax catches malformed embedded snippets
// without executing any of the user-facing setup commands.
func TestFedoraGuideCommandsHaveShellSyntax(t *testing.T) {
	var commands []string
	for _, line := range strings.Split(fedoraGettingStarted, "\n") {
		if strings.HasPrefix(line, "  ") {
			commands = append(commands, strings.TrimPrefix(line, "  "))
		}
	}
	command := exec.Command("sh", "-n")
	command.Stdin = strings.NewReader(strings.Join(commands, "\n") + "\n")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("guide syntax: %v\n%s", err, output)
	}
}

// TestFedoraUserSupportNativeIntegration uses only an already-existing tools
// image and a new disposable volume. It stages through the production helper,
// simulates Anaconda's base-image exclusions, creates an actual Linux user from
// skel, and verifies malformed retained payloads fail. It never builds an image
// or reads/mutates an existing producer volume.
func TestFedoraUserSupportNativeIntegration(t *testing.T) {
	image := os.Getenv("LEXR_TEST_FEDORA_USER_SUPPORT_IMAGE")
	if image == "" {
		t.Skip("set LEXR_TEST_FEDORA_USER_SUPPORT_IMAGE to an existing Fedora tools image")
	}
	// Docker Desktop may not share the host's default temporary directory.
	// An explicit exchange parent keeps this small fixture outside the source.
	workspace, err := os.MkdirTemp(os.Getenv("LEXR_TEST_FEDORA_USER_SUPPORT_WORKSPACE"), ".fedora-user-support-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	docker := platform.NewDocker(nil)
	if _, err := docker.Runner.Capture(ctx, platform.Command{Name: "docker", Args: []string{"image", "inspect", image}}); err != nil {
		t.Fatal(err)
	}
	volume, err := docker.CreateWorkVolume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if err := docker.RemoveWorkVolume(cleanup, volume); err != nil {
			t.Errorf("remove disposable test volume %s: %v", volume, err)
		}
	})
	run := func(script string) {
		t.Helper()
		if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", script, "fedora-user-support-test"); err != nil {
			t.Fatal(err)
		}
	}
	reset := func() {
		t.Helper()
		run(`rm -rf /linux-work/rootfs /linux-work/installed /linux-work/base-rootfs
mkdir -p /linux-work/rootfs/usr/share/lexr /linux-work/rootfs/etc/skel /linux-work/rootfs/run/initramfs/live
printf 'USB only\n' > /linux-work/rootfs/run/initramfs/live/not-installed
`)
	}
	policyDirectory := os.Getenv("LEXR_TEST_FEDORA_USER_SUPPORT_SELINUX")
	if policyDirectory != "" {
		for _, name := range []string{"file_contexts", "file_contexts.subs_dist", "policy.35"} {
			data, err := os.ReadFile(filepath.Join(policyDirectory, name))
			if err != nil {
				t.Fatal(err)
			}
			writeUserSupportFixture(t, filepath.Join(workspace, "selinux", name), data, 0o644)
		}
	}
	for _, included := range []bool{false, true} {
		manifest := fedoraUserSupportFixture(t, workspace, included)
		reset()
		if err := installFedoraUserSupport(ctx, docker, image, workspace, volume, manifest); err != nil {
			t.Fatalf("stage included=%t: %v", included, err)
		}
		if policyDirectory != "" {
			// A tiny EROFS fixture checks the exact source policy and metadata
			// round trip without cloning or repacking a producer filesystem.
			if err := docker.RunInWorkspaceVolumePreservingXattrs(ctx, image, workspace, volume,
				"bash", "-ceu", `setfiles -F -r /linux-work/rootfs -c /work/selinux/policy.35 /work/selinux/file_contexts /linux-work/rootfs
mkfs.erofs -Efragments -C 1048576 -z lzma,level=6 --file-contexts=/work/selinux/file_contexts /linux-work/support-test.erofs /linux-work/rootfs
mv /linux-work/rootfs /linux-work/labelled-rootfs
`, "fedora-support-selinux-test"); err != nil {
				t.Fatal(err)
			}
			if err := docker.RunInWorkspaceVolumePreservingXattrs(ctx, image, workspace, volume, erofsExtractionArguments("/linux-work/support-test.erofs", "/linux-work/rootfs")...); err != nil {
				t.Fatal(err)
			}
			if err := validateFedoraUserSupportContexts(ctx, docker, image, workspace, volume, included); err != nil {
				t.Fatal(err)
			}
			run("rm -rf /linux-work/labelled-rootfs /linux-work/support-test.erofs")
			t.Logf("Fedora source SELinux policy and EROFS round trip passed, companion=%t", included)
		}
		// Copy from the prepared base with the actual Anaconda root-copy
		// exclusions. Tar supplies the same file-selection test without adding
		// a package dependency or running Anaconda in the test container.
		run(`mkdir /linux-work/installed
tar -C /linux-work/rootfs --exclude='./dev' --exclude='./proc' --exclude='./tmp/*' --exclude='./sys' --exclude='./run' --exclude='./boot/*rescue*' --exclude='./boot/loader' --exclude='./boot/efi' --exclude='./boot/grub2' --exclude='./etc/sysconfig' --exclude='./usr/lib/grub' --exclude='./etc/machine-id' --exclude='./etc/machine-info' -cf /linux-work/root-copy.tar .
tar -C /linux-work/installed -xf /linux-work/root-copy.tar
rm /linux-work/root-copy.tar
test ! -e /linux-work/installed/run/initramfs/live/not-installed
mv /linux-work/rootfs /linux-work/base-rootfs
mv /linux-work/installed /linux-work/rootfs
cp /etc/passwd /etc/group /etc/shadow /etc/gshadow /etc/login.defs /linux-work/rootfs/etc/
mkdir -p /linux-work/rootfs/etc/default /linux-work/rootfs/home
cp /etc/default/useradd /linux-work/rootfs/etc/default/useradd
sed -i 's/^CREATE_MAIL_SPOOL=.*/CREATE_MAIL_SPOOL=no/' /linux-work/rootfs/etc/default/useradd
useradd --prefix /linux-work/rootfs --create-home --no-user-group --gid 0 --uid 29991 --no-log-init --shell /bin/bash lexr-guide-test
cmp /linux-work/rootfs/home/lexr-guide-test/Desktop/LEXR_GETTING_STARTED.txt /work/fedora-getting-started.txt
test "$(stat -c '%u' /linux-work/rootfs/home/lexr-guide-test/Desktop/LEXR_GETTING_STARTED.txt)" = 29991
`)
		if err := validateFedoraUserSupport(ctx, docker, image, workspace, volume, manifest); err != nil {
			t.Fatalf("installed retention included=%t: %v", included, err)
		}
		if err := installFedoraUserSupport(ctx, docker, image, workspace, volume, manifest); err == nil {
			t.Fatal("conflicting retained destination was overwritten")
		}
		if included {
			// Model a Linux host user's export directory on the native volume.
			// A payload link must not change its root-owned target, and the
			// unprivileged owner must be able to remove every exported directory.
			run(`mkdir -p /linux-work/ownership-parent/export
chown 29992:29992 /linux-work/ownership-parent /linux-work/ownership-parent/export
printf protected > /linux-work/protected-target
ln -s /linux-work/protected-target /linux-work/rootfs/usr/share/lexr/fedora-media/sp11/companion/target-link
`)
			if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", validateFedoraUserSupportScript, "export-ownership-test", "included", "/linux-work/ownership-parent/export"); err != nil {
				t.Fatal(err)
			}
			run(`test "$(stat -c '%u:%g' /linux-work/protected-target)" = 0:0
test -z "$(find /linux-work/ownership-parent/export \( ! -uid 29992 -o ! -gid 29992 \) -print)"
setpriv --reuid=29992 --regid=29992 --clear-groups --inh-caps=-all rm -rf /linux-work/ownership-parent/export
test ! -e /linux-work/ownership-parent/export
rm /linux-work/rootfs/usr/share/lexr/fedora-media/sp11/companion/target-link
mkdir -p /linux-work/ownership-parent/export/root-owned-child
printf conflict > /linux-work/ownership-parent/export/companion
chown 29992:29992 /linux-work/ownership-parent/export
`)
			if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", validateFedoraUserSupportScript, "export-failure-cleanup-test", "included", "/linux-work/ownership-parent/export"); err == nil {
				t.Fatal("expected copy conflict")
			}
			run(`setpriv --reuid=29992 --regid=29992 --clear-groups --inh-caps=-all rm -rf /linux-work/ownership-parent/export
test ! -e /linux-work/ownership-parent/export
`)
		}
		for _, mutation := range []string{"guide", "manifest", "extra", "link", "special-mode"} {
			if mutation == "special-mode" && !included {
				continue
			}
			reset()
			if err := installFedoraUserSupport(ctx, docker, image, workspace, volume, manifest); err != nil {
				t.Fatal(err)
			}
			script := map[string]string{
				"special-mode": "chmod 4755 /linux-work/rootfs/usr/share/lexr/fedora-media/sp11/companion/bin/linux-arm64/lexr",
				"guide":        "rm /linux-work/rootfs/etc/skel/Desktop/LEXR_GETTING_STARTED.txt",
				"manifest":     "printf changed > /linux-work/rootfs/usr/share/lexr/fedora-media/sp11/lexr-manifest.json",
				"extra":        "mkdir -p /linux-work/rootfs/usr/share/lexr/fedora-media/sp11/companion; printf extra > /linux-work/rootfs/usr/share/lexr/fedora-media/sp11/companion/extra",
				"link":         "rm /linux-work/rootfs/etc/skel/Desktop/LEXR_GETTING_STARTED.txt; ln -s /work/fedora-getting-started.txt /linux-work/rootfs/etc/skel/Desktop/LEXR_GETTING_STARTED.txt",
			}[mutation]
			run(script)
			if err := validateFedoraUserSupport(ctx, docker, image, workspace, volume, manifest); err == nil {
				t.Fatalf("accepted %s mutation, included=%t", mutation, included)
			}
		}
	}
}

// fedoraUserSupportFixture creates a small closed companion, including a static
// AArch64 ELF header that is inspected but never executed by these tests.
func fedoraUserSupportFixture(t *testing.T, workspace string, included bool) imagecontract.Manifest {
	t.Helper()
	manifest := completeFedoraManifestFixture()
	root := filepath.Join(workspace, "sp11/companion")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if !included {
		return manifest
	}
	elfBytes := make([]byte, 64)
	copy(elfBytes, []byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), 1})
	binary.LittleEndian.PutUint16(elfBytes[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(elfBytes[18:], uint16(elf.EM_AARCH64))
	binary.LittleEndian.PutUint32(elfBytes[20:], 1)
	binary.LittleEndian.PutUint16(elfBytes[52:], 64)
	file := func(relative string, data []byte, mode os.FileMode) imagecontract.ArtifactRecord {
		writeUserSupportFixture(t, filepath.Join(root, relative), data, mode)
		id := imagecontract.IdentifyBytes(data)
		return imagecontract.ArtifactRecord{Path: "sp11/companion/" + relative, SHA256: id.SHA256, Size: id.Size}
	}
	executable := file("bin/linux-arm64/lexr", elfBytes, 0o755)
	source := file("source/lexr_test_source.tar.gz", []byte("source fixture"), 0o644)
	manifest.CompanionBundle = imagecontract.CompanionBundleRecord{
		Included: true, Root: companion.ISOFilesystemRoot, ProjectLicence: "not-declared",
		Tool:          &imagecontract.ToolIdentityRecord{Version: "test", Commit: "fixture", BuildDate: "2026-09-09T00:00:00Z"},
		Executable:    &imagecontract.ExecutableArtifactRecord{Artifact: executable, OperatingSystem: "linux", Architecture: "arm64", Format: "ELF", Mode: "0755"},
		SourceArchive: &source,
		Catalogues:    []imagecontract.ArtifactRecord{file("catalogues/supported-isos.json", []byte("{}\n"), 0o644), file("catalogues/supported-userspace.json", []byte("{}\n"), 0o644)},
		Userspace:     []imagecontract.OfflineUserspaceRecord{},
	}
	if err := companion.ValidateDirectory(manifest.CompanionBundle, root); err != nil {
		t.Fatal(err)
	}
	return manifest
}

// writeUserSupportFixture writes only beneath each test's private directory.
func writeUserSupportFixture(t *testing.T, name string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, mode); err != nil {
		t.Fatal(err)
	}
}
