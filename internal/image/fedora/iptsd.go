package fedora

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/platform"
	userspaceinstall "github.com/ooaklee/lexr.sh/internal/userspace/install"
	userspaceiptsd "github.com/ooaklee/lexr.sh/internal/userspace/iptsd"
)

const (
	// fedoraIPTSDPackageName is the sole package identity accepted in the live root.
	fedoraIPTSDPackageName = "lexr-sp11-iptsd"
	// fedoraIPTSDRPMName is the stable binary RPM name exposed on generated media.
	fedoraIPTSDRPMName = "lexr-sp11-iptsd.aarch64.rpm"
	// fedoraIPTSDRPMPath is the manifest-relative binary RPM location.
	fedoraIPTSDRPMPath = "sp11/fedora/" + fedoraIPTSDRPMName
	// fedoraIPTSDSRPMName is the stable source RPM name exposed on generated media.
	fedoraIPTSDSRPMName = "lexr-sp11-iptsd.src.rpm"
	// fedoraIPTSDSRPMPath is the manifest-relative source RPM location.
	fedoraIPTSDSRPMPath = "sp11/fedora/" + fedoraIPTSDSRPMName
	// iptsdReleaseArchive is the exact Fedora-capable source companion filename.
	iptsdReleaseArchive = "sp11-iptsd-3.1.0-sp11.2-arm64.tar.xz"
	// Fedora's udev rules match the Linux HID parent identifier, whose bus,
	// vendor, and product components are colon-separated. Keep validation tied
	// to that kernel representation instead of a USB-style vendor/product path.
	fedoraIPTSDHID0C80 = "001C:045E:0C80"
	// fedoraIPTSDHID0C83 is the second maintained Surface Pro 11 digitiser ID.
	fedoraIPTSDHID0C83 = "001C:045E:0C83"
)

// fedoraIPTSDSource is present only when the already trust-bound companion
// carries the complete corresponding-source IPTSD release.
type fedoraIPTSDSource struct {
	Included      bool
	Archive       imagecontract.ArtifactRecord
	ExtractedRoot string
}

// prepareFedoraIPTSDSource securely extracts and revalidates the exact source-
// bearing companion archive before any RPM tooling sees it.
func prepareFedoraIPTSDSource(ctx context.Context, workspace string, record imagecontract.CompanionBundleRecord) (fedoraIPTSDSource, error) {
	if err := companion.ValidateRecord(record); err != nil {
		return fedoraIPTSDSource{}, fmt.Errorf("validate companion before Fedora IPTSD build: %w", err)
	}
	userspace, included, err := fedoraIPTSDUserspace(record)
	if err != nil || !included {
		return fedoraIPTSDSource{}, err
	}
	archiveRecord, err := fedoraIPTSDArchiveRecord(userspace)
	if err != nil {
		return fedoraIPTSDSource{}, err
	}
	archivePath := filepath.Join(workspace, filepath.FromSlash(archiveRecord.Path))
	if err := verifyStagedArtifact(archivePath, archiveRecord); err != nil {
		return fedoraIPTSDSource{}, fmt.Errorf("verify staged IPTSD release archive: %w", err)
	}
	extractionRoot := filepath.Join(workspace, "fedora-iptsd-extracted")
	_, releaseRoot, err := extractFedoraIPTSDPackageSource(ctx, archivePath, extractionRoot)
	if err != nil {
		return fedoraIPTSDSource{}, err
	}
	if err := verifyStagedArtifact(archivePath, archiveRecord); err != nil {
		return fedoraIPTSDSource{}, fmt.Errorf("reverify staged IPTSD release archive: %w", err)
	}
	return fedoraIPTSDSource{Included: true, Archive: archiveRecord, ExtractedRoot: releaseRoot}, nil
}

// extractFedoraIPTSDPackageSource securely extracts one new private root and
// applies the Fedora-specific enriched archive contract before returning any
// template bytes to the caller.
func extractFedoraIPTSDPackageSource(ctx context.Context, archivePath, extractionRoot string) (userspaceiptsd.FedoraPackageSource, string, error) {
	if err := os.Mkdir(extractionRoot, 0o700); err != nil {
		return userspaceiptsd.FedoraPackageSource{}, "", fmt.Errorf("create Fedora IPTSD extraction root: %w", err)
	}
	if err := (userspaceinstall.SecureXZTarExtractor{}).Extract(ctx, archivePath, extractionRoot); err != nil {
		return userspaceiptsd.FedoraPackageSource{}, "", fmt.Errorf("extract Fedora IPTSD source release: %w", err)
	}
	releaseRoot := filepath.Join(extractionRoot, userspaceiptsd.ArchiveRoot)
	packageSource, err := userspaceiptsd.ValidateFedoraPackageSource(releaseRoot)
	if err != nil {
		return userspaceiptsd.FedoraPackageSource{}, "", fmt.Errorf("validate extracted Fedora IPTSD source release: %w", err)
	}
	return packageSource, releaseRoot, nil
}

// fedoraIPTSDArchiveRecord selects exactly one Fedora-capable source release
// from the already validated companion inventory.
func fedoraIPTSDArchiveRecord(userspace imagecontract.OfflineUserspaceRecord) (imagecontract.ArtifactRecord, error) {
	expectedArchivePath := path.Join(userspace.Root, iptsdReleaseArchive)
	var selected imagecontract.ArtifactRecord
	found := false
	for _, candidate := range userspace.Artifacts {
		if candidate.Path != expectedArchivePath {
			continue
		}
		if found {
			return imagecontract.ArtifactRecord{}, errors.New("IPTSD companion repeats its Fedora-capable source release archive")
		}
		selected, found = candidate, true
	}
	if !found {
		return imagecontract.ArtifactRecord{}, errors.New("IPTSD companion omits its Fedora-capable source release archive")
	}
	return selected, nil
}

// fedoraIPTSDUserspace returns the one exact offline release that authorises a
// native RPM. Merely including the companion CLI does not imply IPTSD support.
func fedoraIPTSDUserspace(record imagecontract.CompanionBundleRecord) (imagecontract.OfflineUserspaceRecord, bool, error) {
	var selected imagecontract.OfflineUserspaceRecord
	found := false
	for _, userspace := range record.Userspace {
		if userspace.Component != companion.IPTSDOfflineComponentID {
			continue
		}
		if found {
			return imagecontract.OfflineUserspaceRecord{}, false, errors.New("companion repeats the IPTSD userspace release")
		}
		selected, found = userspace, true
	}
	return selected, found, nil
}

// verifyStagedArtifact closes the companion-to-RPM handoff around one regular,
// immutable path and its already compiled digest and size authority.
func verifyStagedArtifact(hostPath string, record imagecontract.ArtifactRecord) error {
	if err := imagecontract.ValidateArtifactRecord(record); err != nil {
		return err
	}
	info, err := os.Lstat(hostPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() != record.Size {
		return errors.New("staged artifact is not the declared non-symbolic-link regular file")
	}
	digest, err := artifact.HashFile(hostPath)
	if err != nil {
		return err
	}
	if digest != record.SHA256 {
		return fmt.Errorf("staged artifact SHA-256 is %s, expected %s", digest, record.SHA256)
	}
	return nil
}

// stageFedoraIPTSDSpec exclusively materialises the rendered, archive-derived
// OE template after the compiled Fedora support directory has been created.
// A pre-existing path is a workspace-integrity failure, never an overwrite.
func stageFedoraIPTSDSpec(workspace string, spec []byte) error {
	if len(spec) == 0 || len(spec) > 1<<20 {
		return errors.New("rendered Fedora IPTSD RPM spec has an invalid size")
	}
	destination := filepath.Join(workspace, "fedora-support", "iptsd.spec")
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("stage rendered Fedora IPTSD RPM spec: %w", err)
	}
	if _, err := file.Write(spec); err != nil {
		_ = file.Close()
		return fmt.Errorf("write rendered Fedora IPTSD RPM spec: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync rendered Fedora IPTSD RPM spec: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close rendered Fedora IPTSD RPM spec: %w", err)
	}
	if err := verifyFedoraIPTSDSpec(destination, spec); err != nil {
		return fmt.Errorf("verify rendered Fedora IPTSD RPM spec: %w", err)
	}
	return nil
}

// verifyFedoraIPTSDSpec binds one staged regular file to the exact bytes
// returned by the validated package-source renderer.
func verifyFedoraIPTSDSpec(path string, expected []byte) error {
	actual, err := readBoundedRegularFile(path, 1<<20)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, expected) {
		return errors.New("staged Fedora IPTSD RPM spec differs from the validated OE template rendering")
	}
	return nil
}

// validateIPTSDNativeRoot binds the independently extracted native RPM back to
// the exact static marker bytes consumed by userspace status recognition. The
// rebuilt executables remain architecture-checked inside the Fedora tool image.
func validateIPTSDNativeRoot(root string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	for _, expected := range userspaceiptsd.FedoraNativeRPMStaticFiles() {
		filePath := filepath.Join(root, filepath.FromSlash(expected.Path))
		info, err := os.Lstat(filePath)
		if err != nil {
			return fmt.Errorf("inspect native IPTSD marker /%s: %w", expected.Path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() != expected.Size || expected.Executable && info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("native IPTSD marker /%s has unexpected type, mode, or size", expected.Path)
		}
		digest, err := artifact.HashFile(filePath)
		if err != nil {
			return fmt.Errorf("hash native IPTSD marker /%s: %w", expected.Path, err)
		}
		if digest != expected.SHA256 {
			return fmt.Errorf("native IPTSD marker /%s SHA-256 is %s, expected %s", expected.Path, digest, expected.SHA256)
		}
	}
	return nil
}

// buildIPTSDRPM rebuilds the verified upstream sources and fallbacks as native
// Fedora binary and source packages and exports both for immutable inspection.
func buildIPTSDRPM(ctx context.Context, docker *platform.Docker, image, workspace, volume string, source fedoraIPTSDSource) error {
	if !source.Included {
		return nil
	}
	expectedRoot, err := filepath.Abs(filepath.Join(workspace, "fedora-iptsd-extracted", userspaceiptsd.ArchiveRoot))
	if err != nil {
		return fmt.Errorf("resolve expected Fedora IPTSD source root: %w", err)
	}
	actualRoot, err := filepath.Abs(source.ExtractedRoot)
	if err != nil || actualRoot != expectedRoot {
		return errors.Join(errors.New("Fedora IPTSD source root is outside the private remaster workspace"), err)
	}
	packageSource, err := userspaceiptsd.ValidateFedoraPackageSource(actualRoot)
	if err != nil {
		return fmt.Errorf("revalidate Fedora IPTSD source immediately before RPM build: %w", err)
	}
	if err := stageFedoraIPTSDSpec(workspace, packageSource.RenderedSpec); err != nil {
		return err
	}
	const script = `payload=/work/fedora-iptsd-extracted/sp11-iptsd-v1/payload/iptsd-sp11
integration=/work/fedora-iptsd-extracted/sp11-iptsd-v1/userspace/iptsd-sp11
top=/linux-work/iptsd-rpmbuild
source_root=/linux-work/lexr-sp11-iptsd-source
rpms=/linux-work/iptsd-rpms
commit=$1

[ "$(grep -c '^IPTSD_VERSION=' "$integration/SOURCE.env")" -eq 1 ]
[ "$(grep -c '^IPTSD_COMMIT=' "$integration/SOURCE.env")" -eq 1 ]
[ "$(grep -c '^SOURCE_DATE_EPOCH=' "$payload/BUILD.env")" -eq 1 ]
[ "$(sed -n 's/^IPTSD_VERSION=//p' "$integration/SOURCE.env")" = '3.1.0' ]
[ "$(sed -n 's/^IPTSD_COMMIT=//p' "$integration/SOURCE.env")" = "$commit" ]
epoch=$(sed -n 's/^SOURCE_DATE_EPOCH=//p' "$payload/BUILD.env")
case "$epoch" in ''|*[!0-9]*) exit 1 ;; esac
[ "$epoch" -gt 0 ]

rm -rf -- "$top" "$source_root" "$rpms"
mkdir -p "$top/BUILD" "$top/BUILDROOT" "$top/RPMS" "$top/SOURCES" "$top/SPECS" "$top/SRPMS" \
	"$source_root/source/subprojects/packagecache" "$source_root/integration" "$source_root/licenses" "$rpms"
tar -xzf "$payload/sources/iptsd-$commit.tar.gz" --strip-components=1 -C "$source_root/source"
for wrap in cli11 eigen fmt inih microsoft-gsl spdlog; do
	install -m 0644 "$payload/sources/$wrap.wrap" "$source_root/source/subprojects/$wrap.wrap"
done
find "$payload/sources" -maxdepth 1 -type f \
	! -name '*.wrap' ! -name "iptsd-$commit.tar.gz" \
	-exec cp --no-preserve=ownership {} "$source_root/source/subprojects/packagecache/" \;
cp -a --no-preserve=ownership "$integration/." "$source_root/integration/"
cp -a --no-preserve=ownership "$payload/licenses/." "$source_root/licenses/"
install -m 0644 "$payload/SOURCE.env" "$source_root/SOURCE.env"
find "$source_root" -exec touch -h -d "@$epoch" {} +

source_tar="$top/SOURCES/lexr-sp11-iptsd-source.tar.xz"
	tar --sort=name --owner=0 --group=0 --numeric-owner \
		--mtime="@$epoch" -C /linux-work -cJf "$source_tar" lexr-sp11-iptsd-source
	touch -d "@$epoch" "$source_tar"
	tar -xJOf "$source_tar" \
		lexr-sp11-iptsd-source/integration/packaging/fedora/lexr-sp11-iptsd.spec.in \
		| cmp - "$integration/packaging/fedora/lexr-sp11-iptsd.spec.in"
	install -m 0644 /work/fedora-support/iptsd.spec "$top/SPECS/lexr-sp11-iptsd.spec"

export SOURCE_DATE_EPOCH="$epoch"
rpmbuild -ba --target aarch64 "$top/SPECS/lexr-sp11-iptsd.spec" \
	--define "_topdir $top" \
	--define "_rpmdir $rpms" \
	--define "_buildhost lexr.invalid" \
	--define "_source_date_epoch $epoch" \
	--undefine "_source_date_epoch_from_changelog" \
	--define "clamp_mtime_to_source_date_epoch 1" \
	--define "use_source_date_epoch_as_buildtime 1" \
	--define "_smp_mflags -j8"

set -- $(find "$rpms" -type f -name 'lexr-sp11-iptsd-*.rpm' -print)
[ "$#" -eq 1 ]
rpm_path=$1
set -- $(find "$top/SRPMS" -type f -name 'lexr-sp11-iptsd-*.src.rpm' -print)
[ "$#" -eq 1 ]
srpm_path=$1
rpm -K --nosignature "$rpm_path"
rpm -K --nosignature "$srpm_path"
[ "$(rpm -qp --qf '%{NAME}:%{VERSION}:%{ARCH}' "$rpm_path")" = 'lexr-sp11-iptsd:3.1.0:aarch64' ]
[ "$(rpm -qp --qf '%{NAME}:%{VERSION}:%{ARCH}' "$srpm_path")" = 'lexr-sp11-iptsd:3.1.0:aarch64' ]
[ "$(rpm -qp --qf '%{SOURCERPM}' "$rpm_path")" = 'lexr-sp11-iptsd-3.1.0-1.sp11.fc44.src.rpm' ]
rpm -qp --requires "$rpm_path" | grep -Fx systemd-udev
cat > /linux-work/iptsd-expected-files.txt <<'FILES'
/usr/lib/systemd/system-sleep/sp11-iptsd-restart
/usr/lib/systemd/system/sp11-iptsd@.service
/usr/lib/udev/rules.d/70-sp11-iptsd.rules
/usr/libexec/sp11-iptsd
/usr/libexec/sp11-iptsd-check-device
/usr/share/doc/lexr-sp11-iptsd
/usr/share/doc/lexr-sp11-iptsd/README.md
/usr/share/doc/lexr-sp11-iptsd/SOURCE.env
/usr/share/iptsd/surface-pro-11-0c80.conf
/usr/share/iptsd/surface-pro-11-0c83.conf
/usr/share/licenses/lexr-sp11-iptsd
/usr/share/licenses/lexr-sp11-iptsd/COPYING.Eigen.APACHE
/usr/share/licenses/lexr-sp11-iptsd/COPYING.Eigen.BSD
/usr/share/licenses/lexr-sp11-iptsd/COPYING.Eigen.MINPACK
/usr/share/licenses/lexr-sp11-iptsd/COPYING.Eigen.MPL2
/usr/share/licenses/lexr-sp11-iptsd/COPYING.Eigen.README
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.CLI11
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.Eigen
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.Eigen.build
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.Microsoft-GSL
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.Microsoft-GSL.build
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.fmt
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.fmt.build
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.inih
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.integration
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.iptsd
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.spdlog
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.spdlog.build
FILES
LC_ALL=C sort -o /linux-work/iptsd-expected-files.txt /linux-work/iptsd-expected-files.txt
rpm -qpl "$rpm_path" | LC_ALL=C sort > /linux-work/iptsd-actual-files.txt
diff -u /linux-work/iptsd-expected-files.txt /linux-work/iptsd-actual-files.txt
scripts=$(rpm -qp --scripts "$rpm_path")
grep -F '*/001C:045E:0C80.*|*/001C:045E:0C83.*)' <<<"$scripts"
grep -F '/usr/bin/udevadm trigger --action=add --subsystem-match=hidraw' <<<"$scripts"
grep -F -- '--sysname-match="${hidraw##*/}"' <<<"$scripts"

verify=/linux-work/verify-iptsd-rpm
rm -rf -- "$verify"
mkdir -p "$verify"
rpm2cpio "$rpm_path" > /linux-work/iptsd-rpm.cpio
(cd "$verify" && cpio -idm --quiet < /linux-work/iptsd-rpm.cpio)
for binary in sp11-iptsd sp11-iptsd-check-device; do
	file "$verify/usr/libexec/$binary" | grep -Eq 'ELF 64-bit.*(ARM aarch64|AArch64)'
done
! grep -R -E '@(IPTSD|CHECKER|SYSTEMCTL|SYSTEMD_ESCAPE)@' \
	"$verify/usr/lib/systemd" "$verify/usr/lib/udev"
rpm2cpio "$srpm_path" > /linux-work/iptsd-srpm.cpio
	printf '%s\n' './lexr-sp11-iptsd-source.tar.xz' './lexr-sp11-iptsd.spec' \
		> /linux-work/iptsd-expected-srpm-files.txt
	cpio -it --quiet < /linux-work/iptsd-srpm.cpio | LC_ALL=C sort \
		> /linux-work/iptsd-actual-srpm-files.txt
	diff -u /linux-work/iptsd-expected-srpm-files.txt /linux-work/iptsd-actual-srpm-files.txt
	srpm_verify=/linux-work/verify-iptsd-srpm
	rm -rf -- "$srpm_verify"
	mkdir -p "$srpm_verify"
	(cd "$srpm_verify" && cpio -idm --quiet < /linux-work/iptsd-srpm.cpio)
	cmp /work/fedora-support/iptsd.spec "$srpm_verify/lexr-sp11-iptsd.spec"
	tar -xJOf "$srpm_verify/lexr-sp11-iptsd-source.tar.xz" \
		lexr-sp11-iptsd-source/integration/packaging/fedora/lexr-sp11-iptsd.spec.in \
		| cmp - "$integration/packaging/fedora/lexr-sp11-iptsd.spec.in"

[ ! -e /work/lexr-sp11-iptsd.aarch64.rpm ]
[ ! -e /work/lexr-sp11-iptsd.src.rpm ]
cp "$rpm_path" /work/lexr-sp11-iptsd.aarch64.rpm
cp "$srpm_path" /work/lexr-sp11-iptsd.src.rpm
chmod a+r /work/lexr-sp11-iptsd.aarch64.rpm
chmod a+r /work/lexr-sp11-iptsd.src.rpm
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", script, "lexr-fedora-iptsd", userspaceiptsd.SourceCommit); err != nil {
		return fmt.Errorf("build Fedora IPTSD RPMs: %w", err)
	}
	after, err := userspaceiptsd.ValidateFedoraPackageSource(actualRoot)
	if err != nil {
		return fmt.Errorf("revalidate Fedora IPTSD source after RPM build: %w", err)
	}
	if !bytes.Equal(packageSource.Template, after.Template) || !bytes.Equal(packageSource.RenderedSpec, after.RenderedSpec) {
		return errors.New("Fedora IPTSD package source changed during RPM build")
	}
	if err := verifyFedoraIPTSDSpec(filepath.Join(workspace, "fedora-support", "iptsd.spec"), after.RenderedSpec); err != nil {
		return fmt.Errorf("reverify staged Fedora IPTSD RPM spec: %w", err)
	}
	return nil
}

// installIPTSDRPM builds the two source-complete outputs, then registers the
// binary package in the offline Fedora root with normal dependency checking.
func installIPTSDRPM(ctx context.Context, docker *platform.Docker, image, workspace, volume string, source fedoraIPTSDSource) error {
	if err := buildIPTSDRPM(ctx, docker, image, workspace, volume, source); err != nil || !source.Included {
		return err
	}
	const script = `root=/linux-work/rootfs
rpm_path=/work/lexr-sp11-iptsd.aarch64.rpm
! rpm --root "$root" -q iptsd >/dev/null 2>&1
! rpm --root "$root" -q g6-pen >/dev/null 2>&1
# Scriptlets are intentionally deferred: this is an offline root, and its udev
# rule will naturally see the digitizer when the remastered system boots.
rpm --root "$root" --install --replacepkgs --noscripts "$rpm_path"
rpm --root "$root" -q lexr-sp11-iptsd >/dev/null
for owned in \
	/usr/libexec/sp11-iptsd \
	/usr/libexec/sp11-iptsd-check-device \
	/usr/lib/systemd/system/sp11-iptsd@.service \
	/usr/lib/udev/rules.d/70-sp11-iptsd.rules; do
	rpm --root "$root" -q --qf '%{NAME}\n' --file "$owned" | grep -Fx lexr-sp11-iptsd
done
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", script, "lexr-install-fedora-iptsd"); err != nil {
		return fmt.Errorf("install Fedora IPTSD RPM with dependency checks: %w", err)
	}
	return nil
}

// validateIPTSDRPM independently proves the optional ISO-level RPM identity,
// file topology, service/rule rendering, scriptlet scope, and source metadata.
func (v *Validator) validateIPTSDRPM(ctx context.Context, image, workspace string, manifest imagecontract.Manifest) []imagecontract.ValidationCheck {
	userspace, included, err := fedoraIPTSDUserspace(manifest.CompanionBundle)
	if err != nil {
		return []imagecontract.ValidationCheck{{Name: "fedora-native-iptsd-rpm", Passed: false, Details: err.Error()}}
	}
	if !included {
		listing, listErr := v.Docker.CaptureInWorkspace(ctx, image, workspace,
			"xorriso", "-indev", "/work/image.iso", "-ls", "/sp11/fedora")
		passed := listErr == nil && !strings.Contains(string(listing), fedoraIPTSDRPMName) &&
			!strings.Contains(string(listing), fedoraIPTSDSRPMName)
		details := "native IPTSD RPM correctly omitted because no source-bearing iptsd-v1 companion was included"
		if listErr != nil {
			details = listErr.Error()
		}
		return []imagecontract.ValidationCheck{{Name: "fedora-native-iptsd-rpm-omitted", Passed: passed, Details: details}}
	}
	record, found := findEvidenceArtifact(manifest.MediaDiscovery, "installed-iptsd-rpm")
	if !found || record.Path != fedoraIPTSDRPMPath {
		return []imagecontract.ValidationCheck{{Name: "fedora-native-iptsd-rpm", Passed: false, Details: "manifest lacks the exact native IPTSD RPM record"}}
	}
	sourceRecord, sourceFound := findEvidenceArtifact(manifest.MediaDiscovery, "iptsd-source-rpm")
	if !sourceFound || sourceRecord.Path != fedoraIPTSDSRPMPath {
		return []imagecontract.ValidationCheck{{Name: "fedora-native-iptsd-rpm", Passed: false, Details: "manifest lacks the exact native IPTSD source RPM record"}}
	}
	archiveRecord, archiveFoundErr := fedoraIPTSDArchiveRecord(userspace)
	if archiveFoundErr != nil {
		return []imagecontract.ValidationCheck{{Name: "fedora-native-iptsd-rpm", Passed: false, Details: archiveFoundErr.Error()}}
	}
	extractErr := v.Docker.RunInWorkspace(ctx, image, workspace,
		"xorriso", "-osirrox", "on", "-indev", "/work/image.iso",
		"-extract", "/"+record.Path, "/work/iptsd.rpm",
		"-extract", "/"+sourceRecord.Path, "/work/iptsd.src.rpm",
		"-extract", "/"+archiveRecord.Path, "/work/iptsd-source-release.tar.xz")
	identityErr := verifyStagedArtifact(filepath.Join(workspace, "iptsd.rpm"), record)
	sourceIdentityErr := verifyStagedArtifact(filepath.Join(workspace, "iptsd.src.rpm"), sourceRecord)
	archiveIdentityErr := verifyStagedArtifact(filepath.Join(workspace, "iptsd-source-release.tar.xz"), archiveRecord)
	packageSource, _, packageSourceErr := extractFedoraIPTSDPackageSource(
		ctx,
		filepath.Join(workspace, "iptsd-source-release.tar.xz"),
		filepath.Join(workspace, "iptsd-validation-source"),
	)
	metadata, queryErr := v.Docker.CaptureInWorkspace(ctx, image, workspace,
		"bash", "-ceu", `rpm_path=/work/iptsd.rpm
srpm_path=/work/iptsd.src.rpm
rpm -K --nosignature "$rpm_path"
rpm -K --nosignature "$srpm_path"
[ "$(rpm -qp --qf '%{NAME}:%{VERSION}:%{ARCH}' "$rpm_path")" = 'lexr-sp11-iptsd:3.1.0:aarch64' ]
[ "$(rpm -qp --qf '%{NAME}:%{VERSION}:%{ARCH}' "$srpm_path")" = 'lexr-sp11-iptsd:3.1.0:aarch64' ]
[ "$(rpm -qp --qf '%{SOURCERPM}' "$rpm_path")" = 'lexr-sp11-iptsd-3.1.0-1.sp11.fc44.src.rpm' ]
rpm -qp --requires "$rpm_path" | grep -Fx systemd-udev
files=$(rpm -qpl "$rpm_path")
cat > /work/iptsd-expected-files.txt <<'FILES'
/usr/lib/systemd/system-sleep/sp11-iptsd-restart
/usr/lib/systemd/system/sp11-iptsd@.service
/usr/lib/udev/rules.d/70-sp11-iptsd.rules
/usr/libexec/sp11-iptsd
/usr/libexec/sp11-iptsd-check-device
/usr/share/doc/lexr-sp11-iptsd
/usr/share/doc/lexr-sp11-iptsd/README.md
/usr/share/doc/lexr-sp11-iptsd/SOURCE.env
/usr/share/iptsd/surface-pro-11-0c80.conf
/usr/share/iptsd/surface-pro-11-0c83.conf
/usr/share/licenses/lexr-sp11-iptsd
/usr/share/licenses/lexr-sp11-iptsd/COPYING.Eigen.APACHE
/usr/share/licenses/lexr-sp11-iptsd/COPYING.Eigen.BSD
/usr/share/licenses/lexr-sp11-iptsd/COPYING.Eigen.MINPACK
/usr/share/licenses/lexr-sp11-iptsd/COPYING.Eigen.MPL2
/usr/share/licenses/lexr-sp11-iptsd/COPYING.Eigen.README
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.CLI11
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.Eigen
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.Eigen.build
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.Microsoft-GSL
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.Microsoft-GSL.build
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.fmt
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.fmt.build
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.inih
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.integration
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.iptsd
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.spdlog
/usr/share/licenses/lexr-sp11-iptsd/LICENSE.spdlog.build
FILES
LC_ALL=C sort -o /work/iptsd-expected-files.txt /work/iptsd-expected-files.txt
printf '%s\n' "$files" | LC_ALL=C sort > /work/iptsd-actual-files.txt
diff -u /work/iptsd-expected-files.txt /work/iptsd-actual-files.txt
scripts=$(rpm -qp --scripts "$rpm_path")
grep -F '*/001C:045E:0C80.*|*/001C:045E:0C83.*)' <<<"$scripts"
grep -F '/usr/bin/udevadm trigger --action=add --subsystem-match=hidraw' <<<"$scripts"
grep -F -- '--sysname-match="${hidraw##*/}"' <<<"$scripts"
rm -rf /work/iptsd-rpm-extracted
mkdir /work/iptsd-rpm-extracted
rpm2cpio "$rpm_path" > /work/iptsd-rpm.cpio
(cd /work/iptsd-rpm-extracted && cpio -idm --quiet < /work/iptsd-rpm.cpio)
grep -F 'ExecStart=/usr/libexec/sp11-iptsd' /work/iptsd-rpm-extracted/usr/lib/systemd/system/sp11-iptsd@.service
grep -F "$3" /work/iptsd-rpm-extracted/usr/lib/udev/rules.d/70-sp11-iptsd.rules
grep -F "$4" /work/iptsd-rpm-extracted/usr/lib/udev/rules.d/70-sp11-iptsd.rules
! grep -R -E '@(IPTSD|CHECKER|SYSTEMCTL|SYSTEMD_ESCAPE)@' \
	/work/iptsd-rpm-extracted/usr/lib/systemd /work/iptsd-rpm-extracted/usr/lib/udev
rpm2cpio "$srpm_path" > /work/iptsd-validation-srpm.cpio
printf '%s\n' './lexr-sp11-iptsd-source.tar.xz' './lexr-sp11-iptsd.spec' \
	> /work/iptsd-expected-srpm-files.txt
cpio -it --quiet < /work/iptsd-validation-srpm.cpio | LC_ALL=C sort \
	> /work/iptsd-actual-srpm-files.txt
diff -u /work/iptsd-expected-srpm-files.txt /work/iptsd-actual-srpm-files.txt
	rm -rf /work/iptsd-srpm-extracted
	mkdir /work/iptsd-srpm-extracted
	(cd /work/iptsd-srpm-extracted && cpio -idm --quiet < /work/iptsd-validation-srpm.cpio)
	tar -xJOf /work/iptsd-srpm-extracted/lexr-sp11-iptsd-source.tar.xz \
		lexr-sp11-iptsd-source/integration/packaging/fedora/lexr-sp11-iptsd.spec.in \
		> /work/iptsd-srpm-template
	printf 'release=%s source_root=%s\n' "$1" "$2"`,
		"validate-iptsd-rpm", userspace.Release, userspace.Root, fedoraIPTSDHID0C80, fedoraIPTSDHID0C83)
	nativeRootErr := validateIPTSDNativeRoot(filepath.Join(workspace, "iptsd-rpm-extracted"))
	srpmSpec, srpmSpecErr := readBoundedRegularFile(filepath.Join(workspace, "iptsd-srpm-extracted", "lexr-sp11-iptsd.spec"), 1<<20)
	if srpmSpecErr == nil && packageSourceErr == nil && !bytes.Equal(srpmSpec, packageSource.RenderedSpec) {
		srpmSpecErr = errors.New("source RPM spec differs from the validated OE-owned Fedora IPTSD template rendering")
	}
	srpmTemplate, srpmTemplateErr := readBoundedRegularFile(filepath.Join(workspace, "iptsd-srpm-template"), 1<<20)
	if srpmTemplateErr == nil && packageSourceErr == nil && !bytes.Equal(srpmTemplate, packageSource.Template) {
		srpmTemplateErr = errors.New("source RPM archive contains a Fedora IPTSD template different from the validated companion")
	}
	passed := extractErr == nil && identityErr == nil && sourceIdentityErr == nil && archiveIdentityErr == nil &&
		packageSourceErr == nil && queryErr == nil && nativeRootErr == nil && srpmSpecErr == nil && srpmTemplateErr == nil
	details := strings.TrimSpace(string(metadata))
	if !passed {
		details = errors.Join(
			extractErr, identityErr, sourceIdentityErr, archiveIdentityErr, packageSourceErr,
			queryErr, nativeRootErr, srpmSpecErr, srpmTemplateErr,
		).Error()
	}
	return []imagecontract.ValidationCheck{{Name: "fedora-native-iptsd-rpm", Passed: passed, Details: details}}
}

// validateIPTSDLiveRoot proves that the exact ISO RPM owns byte-identical
// native files in the root Anaconda will copy, with resolvable runtime links.
func (v *Validator) validateIPTSDLiveRoot(ctx context.Context, image, workspace, volume string, manifest imagecontract.Manifest) []imagecontract.ValidationCheck {
	_, included, err := fedoraIPTSDUserspace(manifest.CompanionBundle)
	if err != nil {
		return []imagecontract.ValidationCheck{{Name: "fedora-iptsd-live-root", Passed: false, Details: err.Error()}}
	}
	if !included {
		err := v.Docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
			"bash", "-ceu", `root=/linux-work/rootfs
! rpm --root "$root" -q lexr-sp11-iptsd >/dev/null 2>&1
for absent in \
	/usr/libexec/sp11-iptsd \
	/usr/lib/systemd/system/sp11-iptsd@.service \
	/usr/lib/udev/rules.d/70-sp11-iptsd.rules; do
	test ! -e "$root$absent"
done`)
		return []imagecontract.ValidationCheck{{
			Name: "fedora-iptsd-live-root-omitted", Passed: err == nil,
			Details: "no native IPTSD package or files are claimed when the source-bearing companion is absent",
		}}
	}
	output, validateErr := v.Docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", `root=/linux-work/rootfs
rpm_path=/work/iptsd.rpm
rpm --root "$root" -q --qf '%{NAME}:%{VERSION}:%{ARCH}\n' lexr-sp11-iptsd
! rpm --root "$root" -q iptsd >/dev/null 2>&1
! rpm --root "$root" -q g6-pen >/dev/null 2>&1
expected=/linux-work/validate-iptsd-installed
rm -rf -- "$expected"
mkdir -p "$expected"
rpm2cpio "$rpm_path" > /linux-work/validate-iptsd-installed.cpio
(cd "$expected" && cpio -idm --quiet < /linux-work/validate-iptsd-installed.cpio)
for owned in \
	/usr/libexec/sp11-iptsd \
	/usr/libexec/sp11-iptsd-check-device \
	/usr/share/iptsd/surface-pro-11-0c80.conf \
	/usr/share/iptsd/surface-pro-11-0c83.conf \
	/usr/lib/systemd/system/sp11-iptsd@.service \
	/usr/lib/udev/rules.d/70-sp11-iptsd.rules \
	/usr/lib/systemd/system-sleep/sp11-iptsd-restart \
	/usr/share/doc/lexr-sp11-iptsd/SOURCE.env; do
	rpm --root "$root" -q --qf '%{NAME}\n' --file "$owned" | grep -Fx lexr-sp11-iptsd
	test -f "$root$owned"
	test ! -L "$root$owned"
	cmp "$expected$owned" "$root$owned"
done
chroot "$root" /usr/bin/ldd /usr/libexec/sp11-iptsd | tee /linux-work/iptsd-ldd.txt
! grep -F 'not found' /linux-work/iptsd-ldd.txt
chroot "$root" /usr/bin/ldd /usr/libexec/sp11-iptsd-check-device | tee /linux-work/iptsd-check-ldd.txt
! grep -F 'not found' /linux-work/iptsd-check-ldd.txt
grep -F 'ExecStart=/usr/libexec/sp11-iptsd' "$root/usr/lib/systemd/system/sp11-iptsd@.service"
grep -F "$1" "$root/usr/lib/udev/rules.d/70-sp11-iptsd.rules"
grep -F "$2" "$root/usr/lib/udev/rules.d/70-sp11-iptsd.rules"`,
		"validate-iptsd-live-root", fedoraIPTSDHID0C80, fedoraIPTSDHID0C83)
	return []imagecontract.ValidationCheck{{
		Name: "fedora-iptsd-live-root", Passed: validateErr == nil,
		Details: strings.TrimSpace(string(output)),
	}}
}
