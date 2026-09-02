package fedora

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
	userspaceinstall "github.com/ooaklee/lexr.sh/internal/userspace/install"
	userspaceiptsd "github.com/ooaklee/lexr.sh/internal/userspace/iptsd"
)

// TestFedoraIPTSDValidatorUsesKernelHIDIdentifiers prevents validation from
// drifting to USB-style slash-separated identifiers that cannot occur in the
// packaged udev rule's KERNELS match.
func TestFedoraIPTSDValidatorUsesKernelHIDIdentifiers(t *testing.T) {
	t.Parallel()

	for marker, want := range map[string]string{
		fedoraIPTSDHID0C80: "001C:045E:0C80",
		fedoraIPTSDHID0C83: "001C:045E:0C83",
	} {
		if marker != want || strings.Contains(marker, "/") {
			t.Errorf("Fedora IPTSD HID validator marker = %q, want %q", marker, want)
		}
	}
}

// TestFedoraIPTSDUserspaceRequiresAnExplicitRelease keeps a CLI-only companion
// from silently claiming that a native pen package was installed.
func TestFedoraIPTSDUserspaceRequiresAnExplicitRelease(t *testing.T) {
	t.Parallel()

	record := companion.Absent(companion.OmissionReasonNotRequested)
	if _, included, err := fedoraIPTSDUserspace(record); err != nil || included {
		t.Fatalf("fedoraIPTSDUserspace(absent) = included %t, err %v", included, err)
	}
	record.Included = true
	record.Reason = ""
	record.Userspace = []imagecontract.OfflineUserspaceRecord{{
		Component: companion.IPTSDOfflineComponentID, Release: "sp11-iptsd-v2",
	}}
	got, included, err := fedoraIPTSDUserspace(record)
	if err != nil || !included || got.Release != "sp11-iptsd-v2" {
		t.Fatalf("fedoraIPTSDUserspace(included) = %#v, %t, %v", got, included, err)
	}
	record.Userspace = append(record.Userspace, record.Userspace[0])
	if _, _, err := fedoraIPTSDUserspace(record); err == nil {
		t.Fatal("fedoraIPTSDUserspace() accepted a duplicate release")
	}
}

// TestOEIPTSDRPMContract proves the production renderer consumes the exact
// validated OE-owned template instead of a semantically similar local copy.
func TestOEIPTSDRPMContract(t *testing.T) {
	t.Parallel()

	root := os.Getenv("LEXR_TEST_OE_ROOT")
	if root == "" {
		t.Skip("set LEXR_TEST_OE_ROOT to the linux-surface-pro-11-oe checkout")
	}
	integrationRoot := filepath.Join(root, "userspace", "iptsd-sp11")
	if err := userspaceiptsd.ValidateRepositoryIntegration(integrationRoot); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(integrationRoot, filepath.FromSlash(userspaceiptsd.FedoraRPMSpecRelative)))
	if err != nil {
		t.Fatal(err)
	}
	epoch := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC).Unix()
	rendered, err := userspaceiptsd.RenderFedoraRPMSpec(data, epoch)
	if err != nil {
		t.Fatal(err)
	}
	spec := string(rendered)
	for _, shared := range []string{
		"Name:           lexr-sp11-iptsd",
		"Version:        3.1.0",
		"Requires:       systemd-udev",
		"--prefix=/usr",
		"--bindir=libexec",
		"-Dforce_access_checks=true",
		"--force-fallback-for=CLI11,eigen3,fmt,INIReader,Microsoft.GSL,spdlog",
		"%{_unitdir}/sp11-iptsd@.service",
		"%{_udevrulesdir}/70-sp11-iptsd.rules",
		"chmod 0644",
		`hid_parent="$(/usr/bin/readlink -f "$hidraw/device" 2>/dev/null || :)"`,
		"*/001C:045E:0C80.*|*/001C:045E:0C83.*)",
		"/usr/bin/udevadm trigger --action=add --subsystem-match=hidraw",
		`--sysname-match="${hidraw##*/}"`,
		"export SOURCE_DATE_EPOCH=1788220800",
		"* Tue Sep 01 2026 Lexr maintainers",
	} {
		if !strings.Contains(spec, shared) {
			t.Errorf("rendered OE IPTSD RPM contract omits %q", shared)
		}
	}
	if strings.Contains(spec, "/usr/local") || strings.Contains(spec, "@IPTSD_VERSION@") ||
		strings.Contains(spec, "@SOURCE_DATE_EPOCH@") || strings.Contains(spec, "@CHANGELOG_DATE@") {
		t.Fatal("rendered Fedora IPTSD spec contains a forbidden path or unresolved placeholder")
	}
}

// TestIPTSDRPMBuildIntegration optionally rebuilds the exact source-bearing
// release inside the pinned Fedora tool image. When a disposable extracted
// root volume is supplied with explicit mutation consent, it also proves the
// production dependency-checked offline installation and ownership boundary.
func TestIPTSDRPMBuildIntegration(t *testing.T) {
	bundleRoot := os.Getenv("LEXR_TEST_IPTSD_BUNDLE_DIR")
	if bundleRoot == "" {
		t.Skip("set LEXR_TEST_IPTSD_BUNDLE_DIR to the verified sp11-iptsd-v2 bundle")
	}
	archive := filepath.Join(bundleRoot, iptsdReleaseArchive)
	buildRoot := filepath.Clean(filepath.Join("..", "..", "..", "build"))
	if err := os.MkdirAll(buildRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(buildRoot, ".fedora-iptsd-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	extraction := filepath.Join(workspace, "fedora-iptsd-extracted")
	if err := os.Mkdir(extraction, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (userspaceinstall.SecureXZTarExtractor{}).Extract(context.Background(), archive, extraction); err != nil {
		t.Fatal(err)
	}
	releaseRoot := filepath.Join(extraction, userspaceiptsd.ArchiveRoot)
	if _, err := userspaceiptsd.ValidateFedoraPackageSource(releaseRoot); err != nil {
		t.Fatal(err)
	}
	if err := stageFedoraSupport(workspace, kernel.Bundle{ABI: "7.2.0-jg-0sp11v19-qcom-x1e", Version: "7.2.0"}); err != nil {
		t.Fatal(err)
	}
	docker := platform.NewDocker(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := docker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	image, err := docker.EnsureFedoraToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	externalVolume := strings.TrimSpace(os.Getenv("LEXR_TEST_FEDORA_ROOT_VOLUME"))
	volume := externalVolume
	if volume == "" {
		volume, err = docker.CreateWorkVolume(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			if err := docker.RemoveWorkVolume(cleanup, volume); err != nil {
				t.Errorf("remove integration-test volume: %v", err)
			}
		})
	} else if os.Getenv("LEXR_TEST_MUTATE_FEDORA_ROOT_VOLUME") != "1" {
		t.Fatal("set LEXR_TEST_MUTATE_FEDORA_ROOT_VOLUME=1 to confirm the supplied disposable root volume may be modified")
	}
	source := fedoraIPTSDSource{
		Included: true, ExtractedRoot: releaseRoot,
	}
	if externalVolume == "" {
		err = buildIPTSDRPM(ctx, docker, image, workspace, volume, source)
	} else {
		err = installIPTSDRPM(ctx, docker, image, workspace, volume, source)
	}
	if err != nil {
		t.Fatal(err)
	}
	output, err := docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", `rpm -K --nosignature /work/lexr-sp11-iptsd.aarch64.rpm
rpm -K --nosignature /work/lexr-sp11-iptsd.src.rpm
rpm -qp --qf '%{NAME}:%{VERSION}:%{ARCH}\n' /work/lexr-sp11-iptsd.aarch64.rpm
rm -rf -- /work/iptsd-rpm-extracted
mkdir /work/iptsd-rpm-extracted
rpm2cpio /work/lexr-sp11-iptsd.aarch64.rpm > /work/test-iptsd-rpm.cpio
(cd /work/iptsd-rpm-extracted && cpio -idm --quiet < /work/test-iptsd-rpm.cpio)
rpm2cpio /work/lexr-sp11-iptsd.src.rpm > /linux-work/test-iptsd-srpm.cpio
cpio -it --quiet < /linux-work/test-iptsd-srpm.cpio | grep -Fx './lexr-sp11-iptsd-source.tar.xz'`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(output), "lexr-sp11-iptsd") < 2 {
		t.Fatalf("RPM integration output lacks binary/source identity:\n%s", output)
	}
	if err := validateIPTSDNativeRoot(filepath.Join(workspace, "iptsd-rpm-extracted")); err != nil {
		t.Fatalf("rebuilt RPM disagrees with native status marker contract: %v", err)
	}
	if externalVolume != "" {
		installed, err := docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume,
			"bash", "-ceu", `root=/linux-work/rootfs
rpm --root "$root" -q --qf '%{NAME}:%{VERSION}:%{ARCH}\n' lexr-sp11-iptsd
rpm --root "$root" --verify lexr-sp11-iptsd
for owned in \
	/usr/libexec/sp11-iptsd \
	/usr/libexec/sp11-iptsd-check-device \
	/usr/lib/systemd/system/sp11-iptsd@.service \
	/usr/lib/udev/rules.d/70-sp11-iptsd.rules \
	/usr/share/doc/lexr-sp11-iptsd/SOURCE.env; do
	rpm --root "$root" -q --qf '%{NAME}\n' --file "$owned" | grep -Fx lexr-sp11-iptsd
done`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(installed), "lexr-sp11-iptsd:3.1.0:aarch64") {
			t.Fatalf("installed RPM integration output lacks native identity:\n%s", installed)
		}
	}
}
