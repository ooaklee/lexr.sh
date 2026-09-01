package fedora

import (
	"bytes"
	"context"
	"debug/pe"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/plan"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// portableISONameExpression bounds output names that cross the container boundary.
var portableISONameExpression = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~%-]{0,199}$`)

// safeKernelABIExpression bounds ABI values interpolated into generated paths.
var safeKernelABIExpression = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_~-]{0,159}$`)

// Create remasters, validates, and transactionally publishes one Fedora Live
// hybrid ISO. No destination is visible unless the complete structural
// validator accepts the same bytes.
func (r *Remasterer) Create(ctx context.Context, request Request) (result Result, returnErr error) {
	operationPlan, err := BuildPlan(request)
	if err != nil {
		return Result{}, err
	}
	if !validPortableISOOutput(request.OutputISO) {
		return Result{}, errors.New("output ISO must have a bounded portable .iso filename")
	}
	outputAbsolute, err := filepath.Abs(request.OutputISO)
	if err != nil {
		return Result{}, err
	}
	sourceAbsolute, err := filepath.Abs(request.SourceISO)
	if err != nil {
		return Result{}, err
	}
	if samePath(sourceAbsolute, outputAbsolute) {
		return Result{}, errors.New("source and output ISO paths must be different")
	}
	if err := validateBundlePaths(request.Bundle); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(outputAbsolute), 0o755); err != nil {
		return Result{}, fmt.Errorf("create output directory: %w", err)
	}
	for _, destination := range []struct{ path, label string }{
		{outputAbsolute, "output ISO"},
		{outputAbsolute + ".manifest.json", "manifest sidecar"},
		{outputAbsolute + ".journal.json", "execution journal"},
	} {
		if err := imagecontract.RequireAbsentPublication(destination.path, destination.label); err != nil {
			return Result{}, err
		}
	}
	if err := r.Docker.Check(ctx); err != nil {
		return Result{}, err
	}

	workspaceParent := request.WorkspaceRoot
	if workspaceParent == "" {
		workspaceParent = filepath.Dir(outputAbsolute)
	}
	if err := os.MkdirAll(workspaceParent, 0o755); err != nil {
		return Result{}, fmt.Errorf("create workspace root: %w", err)
	}
	workspace, err := os.MkdirTemp(workspaceParent, ".lexr-fedora-remaster-")
	if err != nil {
		return Result{}, fmt.Errorf("create Fedora remaster workspace: %w", err)
	}
	if !request.KeepWorkspace {
		defer os.RemoveAll(workspace)
	}

	journal := plan.NewJournal(operationPlan.Operation)
	workingJournalPath := filepath.Join(workspace, "image-create.journal.json")
	checkpoint := func(step string, digests map[string]string) error {
		journal.Complete(step, digests)
		return journal.Save(workingJournalPath)
	}

	logf(r.Out, "Staging Fedora source image and kernel bundle")
	sourcePath := filepath.Join(workspace, "source.iso")
	if err := stageFile(request.SourceISO, sourcePath); err != nil {
		return Result{}, err
	}
	sourceDigest, err := artifact.HashFile(sourcePath)
	if err != nil {
		return Result{}, err
	}
	if request.SourceSHA256 != "" && !strings.EqualFold(request.SourceSHA256, sourceDigest) {
		return Result{}, fmt.Errorf("source ISO SHA-256 mismatch: expected %s, got %s", request.SourceSHA256, sourceDigest)
	}
	if err := checkpoint("verify-source", map[string]string{"source.iso": sourceDigest}); err != nil {
		return Result{}, err
	}
	if err := stageBundle(request.Bundle, workspace); err != nil {
		return Result{}, err
	}
	if err := stageFedoraSupport(workspace, request.Bundle); err != nil {
		return Result{}, err
	}
	if err := checkpoint("verify-kernel", packageDigests(request.Bundle)); err != nil {
		return Result{}, err
	}

	companionRecord := companion.Absent(companion.OmissionReasonNotRequested)
	if request.Companion.SourceDirectory != "" {
		if r.Companions == nil {
			return Result{}, errors.New("companion builder is unavailable")
		}
		logf(r.Out, "Staging the Linux ARM64 companion bundle")
		companionRequest := request.Companion
		companionRequest.DestinationDirectory = workspace
		companionRecord, err = r.Companions.Build(ctx, companionRequest)
		if err != nil {
			return Result{}, fmt.Errorf("stage companion bundle: %w", err)
		}
	}
	if err := checkpoint("stage-companion", companionDigests(companionRecord)); err != nil {
		return Result{}, err
	}

	logf(r.Out, "Preparing ARM64 EROFS and RPM tooling")
	toolsImage, err := r.Docker.EnsureFedoraToolsImage(ctx)
	if err != nil {
		return Result{}, err
	}
	workVolume, err := r.Docker.CreateWorkVolume(ctx)
	if err != nil {
		return Result{}, err
	}
	removeWorkVolume := func() error {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return r.Docker.RemoveWorkVolume(cleanupContext, workVolume)
	}
	defer func() {
		if returnErr == nil || workVolume == "" {
			return
		}
		if request.KeepWorkspace {
			returnErr = fmt.Errorf("%w (diagnostic Docker volume retained: %s)", returnErr, workVolume)
			return
		}
		if cleanupErr := removeWorkVolume(); cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("temporary Docker volume retained after cleanup failure: %s: %w", workVolume, cleanupErr))
			return
		}
		removed := workVolume
		workVolume = ""
		returnErr = fmt.Errorf("%w (temporary Docker volume removed: %s)", returnErr, removed)
	}()
	if err := checkpoint("prepare-tools", nil); err != nil {
		return Result{}, err
	}

	logf(r.Out, "Extracting Fedora 44 EROFS live root")
	if err := r.Docker.RunInWorkspace(ctx, toolsImage, workspace,
		"xorriso", "-osirrox", "on", "-indev", "/work/source.iso",
		"-extract", "/LiveOS/squashfs.img", "/work/live.erofs",
		"-extract", "/boot/aarch64/loader/linux", "/work/linux-fedora",
		"-extract", "/boot/aarch64/loader/initrd", "/work/initrd-fedora",
		"-extract", "/boot/grub2/grub.cfg", "/work/grub-fedora.cfg",
		"-extract", "/boot/0x503d6c7e", "/work/media-marker",
		"-extract", "/EFI/BOOT/grub.cfg", "/work/esp-grub.cfg"); err != nil {
		return Result{}, fmt.Errorf("extract Fedora ISO inputs: %w", err)
	}
	if err := validateSourceLayout(ctx, r.Docker, toolsImage, workspace); err != nil {
		return Result{}, err
	}
	if err := r.Docker.RunInWorkspaceVolumePreservingXattrs(ctx, toolsImage, workspace, workVolume,
		erofsExtractionArguments("/work/live.erofs", "/linux-work/rootfs")...); err != nil {
		return Result{}, fmt.Errorf("extract Fedora EROFS root: %w", err)
	}
	if err := checkpoint("extract-live-root", nil); err != nil {
		return Result{}, err
	}

	logf(r.Out, "Building and registering Fedora package for kernel %s", request.Bundle.ABI)
	if err := installKernelRPM(ctx, r.Docker, toolsImage, workspace, workVolume, request.Bundle); err != nil {
		return Result{}, err
	}
	if err := checkpoint("install-kernel", nil); err != nil {
		return Result{}, err
	}
	iptsdSource, err := prepareFedoraIPTSDSource(ctx, workspace, companionRecord)
	if err != nil {
		return Result{}, err
	}
	iptsdDigests := map[string]string(nil)
	if iptsdSource.Included {
		logf(r.Out, "Building and installing native Fedora IPTSD package from the verified source-bearing companion")
		if err := installIPTSDRPM(ctx, r.Docker, toolsImage, workspace, workVolume, iptsdSource); err != nil {
			return Result{}, err
		}
		iptsdDigest, err := artifact.HashFile(filepath.Join(workspace, fedoraIPTSDRPMName))
		if err != nil {
			return Result{}, fmt.Errorf("hash native Fedora IPTSD RPM: %w", err)
		}
		iptsdSourceDigest, err := artifact.HashFile(filepath.Join(workspace, fedoraIPTSDSRPMName))
		if err != nil {
			return Result{}, fmt.Errorf("hash native Fedora IPTSD source RPM: %w", err)
		}
		iptsdDigests = map[string]string{
			fedoraIPTSDRPMName:  iptsdDigest,
			fedoraIPTSDSRPMName: iptsdSourceDigest,
		}
	}
	if err := checkpoint("install-userspace", iptsdDigests); err != nil {
		return Result{}, err
	}
	if err := checkpoint("assemble-initramfs-root", nil); err != nil {
		return Result{}, err
	}

	logf(r.Out, "Generating non-host-only dracut-live initramfs for %s", request.Bundle.ABI)
	if err := buildLiveInitramfs(ctx, r.Docker, toolsImage, workspace, workVolume, request.Bundle.ABI); err != nil {
		return Result{}, err
	}
	if err := copyBootArtifacts(ctx, r.Docker, toolsImage, workspace, workVolume, request.Bundle); err != nil {
		return Result{}, err
	}
	if err := checkpoint("build-initramfs", nil); err != nil {
		return Result{}, err
	}
	if err := checkpoint("bind-live-media", map[string]string{"iso-volume-label": SourceVolumeID}); err != nil {
		return Result{}, err
	}
	if err := validateStubbleKernel(ctx, r.Docker, toolsImage, workspace, request.Bundle.ABI); err != nil {
		return Result{}, err
	}
	if err := checkpoint("pair-device-trees", nil); err != nil {
		return Result{}, err
	}

	logf(r.Out, "Repacking Fedora live root as LZMA EROFS")
	if err := repackEROFS(ctx, r.Docker, toolsImage, workspace, workVolume); err != nil {
		return Result{}, err
	}
	if err := checkpoint("repack-live-root", nil); err != nil {
		return Result{}, err
	}

	manifest, err := buildEmbeddedManifest(request, workspace, sourceDigest, companionRecord)
	if err != nil {
		return Result{}, err
	}
	manifestBytes, err := serialiseManifest(manifest)
	if err != nil {
		return Result{}, err
	}
	if err := writeSupportFiles(workspace, manifest, manifestBytes, request.Bundle.ABI); err != nil {
		return Result{}, err
	}

	logf(r.Out, "Replaying Fedora hybrid GPT and El Torito boot layout")
	partialName := "output.partial.iso"
	xorrisoArgs := []string{
		"xorriso", "-indev", "/work/source.iso", "-outdev", "/work/" + partialName,
		"-boot_image", "any", "replay", "-volid", SourceVolumeID,
		"-map", "/work/remastered.erofs", "/LiveOS/squashfs.img",
		"-map", "/work/fedora-vmlinuz", "/boot/aarch64/loader/linux",
		"-map", "/work/fedora-initrd", "/boot/aarch64/loader/initrd",
		"-map", "/work/linux-fedora", "/boot/aarch64/loader/linux-fedora",
		"-map", "/work/initrd-fedora", "/boot/aarch64/loader/initrd-fedora",
		"-map", "/work/grub.cfg", "/boot/grub2/grub.cfg",
		"-map", "/work/sp11", "/sp11", "-commit",
	}
	if err := r.Docker.RunInWorkspace(ctx, toolsImage, workspace, xorrisoArgs...); err != nil {
		return Result{}, fmt.Errorf("rebuild Fedora hybrid ISO: %w", err)
	}
	if err := checkpoint("replay-hybrid-boot", nil); err != nil {
		return Result{}, err
	}

	partialPath := filepath.Join(workspace, partialName)
	logf(r.Out, "Validating Fedora hybrid ISO before publication")
	validation, err := NewValidator(r.Docker).Validate(ctx, partialPath)
	if err != nil {
		return Result{}, fmt.Errorf("validate Fedora ISO before publication: %w", err)
	}
	if !validation.Valid {
		return Result{}, errors.New("validate Fedora ISO before publication: validator returned an invalid report")
	}
	manifestIdentity := imagecontract.IdentifyBytes(manifestBytes)
	if validation.ManifestSHA256 != manifestIdentity.SHA256 || validation.ManifestSize != manifestIdentity.Size {
		return Result{}, errors.New("validate Fedora ISO before publication: embedded manifest differs from staged sidecar")
	}
	journal.Output = &plan.OutputRecord{Path: outputAbsolute, SHA256: validation.SHA256, Size: validation.Size}
	if err := checkpoint("validate-output", map[string]string{"output.iso": validation.SHA256}); err != nil {
		return Result{}, err
	}
	publicationJournal := *journal
	publicationJournal.Records = append([]plan.StepRecord(nil), journal.Records...)
	publicationJournal.Complete("publish-output", map[string]string{"output.iso": validation.SHA256})
	publicationJournalPath := filepath.Join(workspace, "image-create.complete.journal.json")
	if err := publicationJournal.Save(publicationJournalPath); err != nil {
		return Result{}, fmt.Errorf("stage completed image journal: %w", err)
	}
	journalBytes, err := os.ReadFile(publicationJournalPath)
	if err != nil {
		return Result{}, fmt.Errorf("read completed image journal: %w", err)
	}
	if !request.KeepWorkspace {
		if err := removeWorkVolume(); err != nil {
			return Result{}, fmt.Errorf("remove completed Fedora workspace volume before publication: %w", err)
		}
		workVolume = ""
	}
	manifestPath, journalPath, err := imagecontract.PublishISOOutputs(
		partialPath, outputAbsolute, manifestBytes, journalBytes,
		imagecontract.PublicationIdentity{SHA256: validation.SHA256, Size: validation.Size},
	)
	if err != nil {
		return Result{}, err
	}

	resultWorkspace, resultVolume := "", ""
	if request.KeepWorkspace {
		resultWorkspace, resultVolume = workspace, workVolume
		logf(r.Out, "Preserving diagnostic workspace %s and Docker volume %s", workspace, workVolume)
	}
	return Result{
		OutputISO: outputAbsolute, ManifestPath: manifestPath, JournalPath: journalPath,
		SHA256: validation.SHA256, Size: validation.Size, WorkspacePath: resultWorkspace,
		WorkspaceVolume: resultVolume, CompanionBundle: companionRecord,
	}, nil
}

// erofsExtractionArguments force root-path traversal around erofs-utils 1.9's
// packed-fragment prepass bug while retaining every Fedora filesystem xattr.
func erofsExtractionArguments(source, destination string) []string {
	return []string{
		"fsck.erofs", "--extract=" + destination, "--path=/",
		"--xattrs", "--preserve", source,
	}
}

// validateSourceLayout confirms Fedora's EROFS, volume-label, and ESP-stub contracts.
func validateSourceLayout(ctx context.Context, docker *platform.Docker, image, workspace string) error {
	filesystem, err := docker.CaptureInWorkspace(ctx, image, workspace, "blkid", "-p", "-s", "TYPE", "-o", "value", "/work/live.erofs")
	if err != nil || strings.TrimSpace(string(filesystem)) != "erofs" {
		return errors.Join(errors.New("Fedora LiveOS/squashfs.img is not an EROFS filesystem"), err)
	}
	pvd, err := docker.CaptureInWorkspace(ctx, image, workspace, "xorriso", "-indev", "/work/source.iso", "-pvd_info")
	if err != nil {
		return fmt.Errorf("inspect Fedora ISO volume descriptor: %w", err)
	}
	if !strings.Contains(string(pvd), "Volume Id    : "+SourceVolumeID) {
		return fmt.Errorf("Fedora ISO volume label is not %q", SourceVolumeID)
	}
	esp, err := os.ReadFile(filepath.Join(workspace, "esp-grub.cfg"))
	if err != nil {
		return err
	}
	if !bytes.Contains(esp, []byte("search --file --set=root /boot/0x503d6c7e")) ||
		!bytes.Contains(esp, []byte("configfile ($root)/boot/grub2/grub.cfg")) {
		return errors.New("Fedora appended-ESP GRUB stub does not select the outer boot configuration")
	}
	marker, err := os.Lstat(filepath.Join(workspace, "media-marker"))
	if err != nil || marker.Mode()&os.ModeSymlink != 0 || !marker.Mode().IsRegular() {
		return errors.Join(errors.New("Fedora outer ISO is missing the regular GRUB search marker"), err)
	}
	return nil
}

// stageFedoraSupport materialises generated RPM and installed-system policy inputs.
func stageFedoraSupport(workspace string, bundle kernel.Bundle) error {
	support := filepath.Join(workspace, "fedora-support")
	if err := os.MkdirAll(support, 0o755); err != nil {
		return err
	}
	policy := expectedBootPolicy(bundle.ABI)
	policyBytes, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	policyBytes = append(policyBytes, '\n')
	files := []struct {
		name string
		mode os.FileMode
		data []byte
	}{
		{"kernel.spec", 0o644, []byte(kernelRPMSpec(bundle.ABI, bundle.Version))},
		{"iptsd.spec", 0o644, []byte(iptsdRPMSpec())},
		{"finalize-installed", 0o755, []byte(installedFinalizeScript(bundle.ABI))},
		{"lexr-sp11-installed-finalize.service", 0o644, []byte(installedFinalizeService())},
		{"grub-defaults", 0o644, []byte(installedGrubDefaults())},
		{"kernel-install.conf", 0o644, []byte(kernelInstallConfiguration())},
		{"kernel-abi", 0o644, []byte(bundle.ABI + "\n")},
		{"boot-policy.json", 0o644, policyBytes},
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(support, file.name), file.data, file.mode); err != nil {
			return fmt.Errorf("stage Fedora support file %s: %w", file.name, err)
		}
	}
	return nil
}

// installKernelRPM converts the verified Debian payload into Fedora-owned lifecycle state.
func installKernelRPM(ctx context.Context, docker *platform.Docker, image, workspace, volume string, bundle kernel.Bundle) error {
	imagePackage, ok := bundle.Package(kernel.RoleImage)
	if !ok {
		return errors.New("kernel bundle has no image package")
	}
	modulesPackage, ok := bundle.Package(kernel.RoleModules)
	if !ok {
		return errors.New("kernel bundle has no modules package")
	}
	const script = `root=/linux-work/rootfs
payload=/linux-work/rpm-payload
top=/linux-work/rpmbuild
rpms=/linux-work/rpms
abi=$1
version=$2
modules_archive=$3
image_archive=$4
support=/work/fedora-support

verify_deb() {
	archive=$1
	expected=$2
	[ "$(dpkg-deb --field "$archive" Package)" = "$expected" ]
	[ "$(dpkg-deb --field "$archive" Version)" = "$version" ]
	[ "$(dpkg-deb --field "$archive" Architecture)" = arm64 ]
}
verify_deb "$modules_archive" "linux-modules-$abi"
verify_deb "$image_archive" "linux-image-$abi"

rm -rf -- "$payload" "$top" "$rpms"
mkdir -p "$payload" "$top/BUILD" "$top/BUILDROOT" "$top/RPMS" "$top/SOURCES" "$top/SPECS" "$top/SRPMS" "$rpms"
dpkg-deb --extract "$modules_archive" "$payload"
dpkg-deb --extract "$image_archive" "$payload"

kernel="$payload/boot/vmlinuz-$abi"
modules="$payload/usr/lib/modules/$abi"
[ -s "$kernel" ]
[ -d "$modules/kernel" ]
[ -s "$payload/boot/System.map-$abi" ]
[ -s "$payload/boot/config-$abi" ]

# dpkg-deb already extracted the image at its final payload path. Normalize its
# mode in place; copying it onto itself is rejected by GNU install.
chmod 0755 "$kernel"
install -m 0755 "$kernel" "$modules/vmlinuz-dtbloader.efi"
ln -s vmlinuz-dtbloader.efi "$modules/vmlinuz"
install -m 0644 "$payload/boot/System.map-$abi" "$modules/System.map"
install -m 0644 "$payload/boot/config-$abi" "$modules/config"
install -d -m 0755 "$modules/dtb/qcom"
for name in x1e80100-microsoft-denali-oled.dtb x1e80100-microsoft-denali-oled-el2.dtb x1p64100-microsoft-denali.dtb x1p64100-microsoft-denali-el2.dtb; do
	dtb="$payload/usr/lib/firmware/$abi/device-tree/qcom/$name"
	[ -s "$dtb" ]
	install -m 0644 "$dtb" "$modules/dtb/qcom/$name"
done
rm -rf -- "$payload/usr/lib/modprobe.d" "$payload/usr/lib/linux" "$payload/usr/share/doc"
rmdir --ignore-fail-on-non-empty "$payload/usr/share" 2>/dev/null || :

install -d -m 0755 "$payload/etc/kernel" "$payload/usr/lib/lexr/sp11" "$payload/usr/lib/systemd/system/multi-user.target.wants"
install -m 0644 "$support/kernel-install.conf" "$payload/etc/kernel/install.conf"
install -m 0755 "$support/finalize-installed" "$payload/usr/lib/lexr/sp11/finalize-installed"
install -m 0644 "$support/kernel-abi" "$payload/usr/lib/lexr/sp11/kernel-abi"
install -m 0644 "$support/boot-policy.json" "$payload/usr/lib/lexr/sp11/boot-policy.json"
install -m 0644 "$support/grub-defaults" "$payload/usr/lib/lexr/sp11/grub-defaults"
install -m 0644 "$support/lexr-sp11-installed-finalize.service" "$payload/usr/lib/systemd/system/lexr-sp11-installed-finalize.service"
ln -s ../lexr-sp11-installed-finalize.service "$payload/usr/lib/systemd/system/multi-user.target.wants/lexr-sp11-installed-finalize.service"

rpmbuild --define "_topdir $top" --define "_rpmdir $rpms" -bb "$support/kernel.spec"
set -- $(find "$rpms" -type f -name '*.rpm' -print)
[ "$#" -eq 1 ]
rpm_path=$1
[ "$(rpm -qp --qf '%{NAME}:%{ARCH}' "$rpm_path")" = 'lexr-kernel-sp11:aarch64' ]
rpm -qp --provides "$rpm_path" | grep -Fx 'kernel-uname-r'
rpm -qp --provides "$rpm_path" | grep -Fx 'kernel-core-uname-r'
rpm -qp --provides "$rpm_path" | grep -Fx 'installonlypkg(kernel)'

rpm --root "$root" --install --replacepkgs --nodeps --noscripts "$rpm_path"
rpm --root "$root" -q lexr-kernel-sp11 >/dev/null
rpm --root "$root" -q --qf '%{NAME}\n' --whatprovides kernel-uname-r | grep -Fx lexr-kernel-sp11
rpm --root "$root" -q --qf '%{NAME}\n' --file "/boot/vmlinuz-$abi" | grep -Fx lexr-kernel-sp11
rpm --root "$root" -q --qf '%{NAME}\n' --file "/usr/lib/modules/$abi/vmlinuz-dtbloader.efi" | grep -Fx lexr-kernel-sp11
install -m 0644 "$support/grub-defaults" "$root/etc/default/grub"
depmod -a -b "$root" "$abi"
# Anaconda sorts visible /boot/vmlinuz-* candidates oldest-first. Keep Fedora's
# stock kernel only as the outer ISO fallback so the custom RPM is selected.
find "$root/boot" -maxdepth 1 -type f -name 'vmlinuz-*' ! -name "vmlinuz-$abi" -delete
[ "$(find "$root/boot" -maxdepth 1 -type f -name 'vmlinuz-*' -printf '%f\n')" = "vmlinuz-$abi" ]
cmp "$kernel" "$root/boot/vmlinuz-$abi"
cmp "$kernel" "$root/usr/lib/modules/$abi/vmlinuz-dtbloader.efi"
cp "$rpm_path" /work/lexr-kernel-sp11.aarch64.rpm
chmod a+r /work/lexr-kernel-sp11.aarch64.rpm
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", script, "lexr-fedora-kernel", bundle.ABI, bundle.Version,
		"/work/kernel/"+modulesPackage.Name, "/work/kernel/"+imagePackage.Name); err != nil {
		return fmt.Errorf("build and install Fedora kernel RPM: %w", err)
	}
	return nil
}

// buildLiveInitramfs generates the exact-ABI non-host-only dracut live image.
func buildLiveInitramfs(ctx context.Context, docker *platform.Docker, image, workspace, volume, abi string) error {
	const script = `root=/linux-work/rootfs
abi=$1
cleanup_mounts() {
	for name in run sys proc dev; do
		umount -R "$root/$name" 2>/dev/null || :
	done
}
trap cleanup_mounts EXIT
for name in dev proc sys run; do
	mount --rbind "/$name" "$root/$name"
	mount --make-rslave "$root/$name"
done
rm -f -- "$root/boot/initramfs-$abi.img"
chmod 1777 "$root/var/tmp"
chroot "$root" /usr/bin/dracut --force --reproducible --no-hostonly --no-hostonly-cmdline \
	--install /.profile --add "dmsquash-live livenet pollcdrom" --omit multipath \
	--kver "$abi" "/boot/initramfs-$abi.img"
test -s "$root/boot/initramfs-$abi.img"
chroot "$root" /usr/bin/lsinitrd -m "/boot/initramfs-$abi.img" | grep -Fx dmsquash-live
chroot "$root" /usr/bin/lsinitrd "/boot/initramfs-$abi.img" | grep -F "usr/lib/modules/$abi/" >/dev/null
`
	if err := docker.RunInWorkspaceVolumePreservingXattrs(ctx, image, workspace, volume,
		"bash", "-ceu", script, "lexr-fedora-dracut", abi); err != nil {
		return fmt.Errorf("generate Fedora dracut-live initramfs: %w", err)
	}
	return nil
}

// copyBootArtifacts exports validated kernel, initramfs, and DTB bytes from Linux storage.
func copyBootArtifacts(ctx context.Context, docker *platform.Docker, image, workspace, volume string, bundle kernel.Bundle) error {
	args := []string{"bash", "-ceu", `root=/linux-work/rootfs
abi=$1
install -m 0644 "$root/boot/vmlinuz-$abi" /work/fedora-vmlinuz
install -m 0644 "$root/boot/initramfs-$abi.img" /work/fedora-initrd
install -d -m 0755 /work/sp11/dtb
for name in x1e80100-microsoft-denali-oled.dtb x1p64100-microsoft-denali.dtb; do
	install -m 0644 "$root/usr/lib/firmware/$abi/device-tree/qcom/$name" "/work/sp11/dtb/$name"
done
chmod -R a+rX /work/sp11 /work/fedora-vmlinuz /work/fedora-initrd
`, "lexr-copy-fedora-boot", bundle.ABI}
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, args...); err != nil {
		return fmt.Errorf("copy Fedora boot artefacts: %w", err)
	}
	return nil
}

// validateStubbleKernel proves the custom PE carries required X1E auto-DTB metadata.
func validateStubbleKernel(ctx context.Context, docker *platform.Docker, image, workspace, abi string) error {
	x1eDTB := filepath.Join(workspace, "sp11", "dtb", "x1e80100-microsoft-denali-oled.dtb")
	matches, sectionCount, err := matchingPESectionPayloads(filepath.Join(workspace, "fedora-vmlinuz"), ".dtbauto", x1eDTB)
	if err != nil {
		return fmt.Errorf("inspect Stubble X1E auto-DTB payload: %w", err)
	}
	if matches != 1 {
		return fmt.Errorf("custom Stubble kernel contains %d exact X1E/OLED DTB matches across %d .dtbauto sections; expected 1", matches, sectionCount)
	}
	unameMatches, unameSections, err := matchingPESectionValues(
		filepath.Join(workspace, "fedora-vmlinuz"), ".uname", []byte(abi))
	if err != nil {
		return fmt.Errorf("inspect Stubble uname payload: %w", err)
	}
	if unameMatches != 1 || unameSections != 1 {
		return fmt.Errorf("custom Stubble kernel contains %d exact ABI matches across %d .uname sections; expected exactly 1", unameMatches, unameSections)
	}
	sectionOutput, err := docker.CaptureInWorkspace(ctx, image, workspace, "objdump", "-h", "/work/fedora-vmlinuz")
	if err != nil {
		return fmt.Errorf("inspect Stubble kernel sections: %w", err)
	}
	text := string(sectionOutput)
	for _, section := range []string{".linux", ".hwids", ".dtbauto", ".uname", ".sbat"} {
		if !strings.Contains(text, section) {
			return fmt.Errorf("custom kernel is missing Stubble PE section %s", section)
		}
	}
	stringsOutput, err := docker.CaptureInWorkspace(ctx, image, workspace, "strings", "/work/fedora-vmlinuz")
	if err != nil {
		return fmt.Errorf("inspect Stubble kernel identities: %w", err)
	}
	identities := string(stringsOutput)
	for _, required := range []string{"Microsoft Surface Pro 11th Edition (OLED)", "microsoft,denali-oled"} {
		if !strings.Contains(identities, required) {
			return fmt.Errorf("custom Stubble kernel does not contain required X1E identity %q", required)
		}
	}
	return nil
}

// matchingPESectionPayloads counts exact payload copies in same-named PE
// sections without trusting strings elsewhere in the embedded kernel blob.
func matchingPESectionPayloads(imagePath, sectionName, payloadPath string) (matches, sections int, returnErr error) {
	payload, err := readBoundedRegularFile(payloadPath, 16<<20)
	if err != nil {
		return 0, 0, err
	}
	return matchingPESectionValues(imagePath, sectionName, payload)
}

// matchingPESectionValues counts sections whose virtual payload is exactly
// expected, excluding PE file-alignment padding and matches elsewhere in the
// embedded kernel image.
func matchingPESectionValues(imagePath, sectionName string, expected []byte) (matches, sections int, returnErr error) {
	if len(expected) == 0 || len(expected) > 16<<20 {
		return 0, 0, errors.New("expected PE section payload is empty or exceeds the inspection bound")
	}
	imageInfo, err := os.Lstat(imagePath)
	if err != nil {
		return 0, 0, err
	}
	if imageInfo.Mode()&os.ModeSymlink != 0 || !imageInfo.Mode().IsRegular() || imageInfo.Size() < 1 || imageInfo.Size() > 2<<30 {
		return 0, 0, errors.New("PE image is not a bounded non-symbolic-link regular file")
	}
	imageFile, err := pe.Open(imagePath)
	if err != nil {
		return 0, 0, err
	}
	defer func() { returnErr = errors.Join(returnErr, imageFile.Close()) }()
	if imageFile.FileHeader.Machine != pe.IMAGE_FILE_MACHINE_ARM64 {
		return 0, 0, errors.New("PE image is not AArch64")
	}
	for _, section := range imageFile.Sections {
		if section.Name != sectionName {
			continue
		}
		sections++
		if uint64(section.VirtualSize) != uint64(len(expected)) || section.VirtualSize > section.Size {
			continue
		}
		data, err := section.Data()
		if err != nil {
			return 0, sections, err
		}
		if uint64(len(data)) < uint64(section.VirtualSize) {
			return 0, sections, errors.New("PE section data is shorter than its declared virtual payload")
		}
		if bytes.Equal(data[:section.VirtualSize], expected) {
			matches++
		}
	}
	return matches, sections, nil
}

// repackEROFS relabels and recreates Fedora's live root with compatible compression.
func repackEROFS(ctx context.Context, docker *platform.Docker, image, workspace, volume string) error {
	const script = `root=/linux-work/rootfs
contexts="$root/etc/selinux/targeted/contexts/files/file_contexts"
policy="$root/etc/selinux/targeted/policy/policy.35"
[ -s "$contexts" ]
[ -s "$policy" ]
chroot "$root" /usr/sbin/setfiles -T 0 -F \
	-c /etc/selinux/targeted/policy/policy.35 \
	-e /proc -e /sys -e /dev -e /run \
	/etc/selinux/targeted/contexts/files/file_contexts /
rm -f -- /linux-work/remastered.erofs
mkfs.erofs -Efragments -C 1048576 -z lzma,level=6 --file-contexts="$contexts" /linux-work/remastered.erofs "$root"
dump.erofs -s /linux-work/remastered.erofs | grep -F 'Filesystem compr_algs:' | grep -F lzma
cp /linux-work/remastered.erofs /work/remastered.erofs
chmod a+r /work/remastered.erofs
`
	if err := docker.RunInWorkspaceVolumePreservingXattrs(ctx, image, workspace, volume,
		"bash", "-ceu", script, "lexr-repack-erofs"); err != nil {
		return fmt.Errorf("repack Fedora EROFS filesystem: %w", err)
	}
	return nil
}

// buildEmbeddedManifest records the complete boot and support payload identity.
func buildEmbeddedManifest(request Request, workspace, sourceDigest string, companionRecord imagecontract.CompanionBundleRecord) (imagecontract.Manifest, error) {
	sourceInfo, err := os.Stat(filepath.Join(workspace, "source.iso"))
	if err != nil {
		return imagecontract.Manifest{}, err
	}
	kernelRecord, err := artifactRecord(filepath.Join(workspace, "fedora-vmlinuz"), "boot/aarch64/loader/linux")
	if err != nil {
		return imagecontract.Manifest{}, err
	}
	initrdRecord, err := artifactRecord(filepath.Join(workspace, "fedora-initrd"), "boot/aarch64/loader/initrd")
	if err != nil {
		return imagecontract.Manifest{}, err
	}
	stockKernelRecord, err := artifactRecord(filepath.Join(workspace, "linux-fedora"), "boot/aarch64/loader/linux-fedora")
	if err != nil {
		return imagecontract.Manifest{}, err
	}
	stockInitrdRecord, err := artifactRecord(filepath.Join(workspace, "initrd-fedora"), "boot/aarch64/loader/initrd-fedora")
	if err != nil {
		return imagecontract.Manifest{}, err
	}
	rpmRecord, err := artifactRecord(
		filepath.Join(workspace, "lexr-kernel-sp11.aarch64.rpm"),
		"sp11/fedora/lexr-kernel-sp11.aarch64.rpm",
	)
	if err != nil {
		return imagecontract.Manifest{}, err
	}
	var dtbs []imagecontract.ArtifactRecord
	for _, dtb := range request.Bundle.DeviceTrees {
		record, err := artifactRecord(filepath.Join(workspace, "sp11", "dtb", filepath.Base(dtb.Path)), "sp11/dtb/"+filepath.Base(dtb.Path))
		if err != nil {
			return imagecontract.Manifest{}, err
		}
		dtbs = append(dtbs, record)
	}
	bootArguments := append([]string(nil), installedBootArguments...)
	bootArguments = append(bootArguments, liveOnlyBootArguments...)
	evidence := []imagecontract.MediaDiscoveryEvidence{
		{Role: "iso-volume-label", Scope: "iso9660-pvd", Value: SourceVolumeID},
		{Role: "live-root", Scope: "grub", Path: "boot/grub2/grub.cfg", Value: "root=live:CDLABEL=" + SourceVolumeID + " rd.live.image"},
		{Role: "installed-kernel-rpm", Scope: "iso9660", Path: rpmRecord.Path, Artifact: &rpmRecord},
		{Role: "stock-fallback-kernel", Scope: "iso9660", Path: stockKernelRecord.Path, Artifact: &stockKernelRecord},
		{Role: "stock-fallback-initramfs", Scope: "iso9660", Path: stockInitrdRecord.Path, Artifact: &stockInitrdRecord},
	}
	_, iptsdIncluded, err := fedoraIPTSDUserspace(companionRecord)
	if err != nil {
		return imagecontract.Manifest{}, err
	}
	if iptsdIncluded {
		iptsdRPMRecord, err := artifactRecord(filepath.Join(workspace, fedoraIPTSDRPMName), fedoraIPTSDRPMPath)
		if err != nil {
			return imagecontract.Manifest{}, err
		}
		iptsdSRPMRecord, err := artifactRecord(filepath.Join(workspace, fedoraIPTSDSRPMName), fedoraIPTSDSRPMPath)
		if err != nil {
			return imagecontract.Manifest{}, err
		}
		evidence = append(evidence, imagecontract.MediaDiscoveryEvidence{
			Role: "installed-iptsd-rpm", Scope: "iso9660", Path: iptsdRPMRecord.Path, Artifact: &iptsdRPMRecord,
		})
		evidence = append(evidence, imagecontract.MediaDiscoveryEvidence{
			Role: "iptsd-source-rpm", Scope: "iso9660", Path: iptsdSRPMRecord.Path, Artifact: &iptsdSRPMRecord,
		})
	}
	return imagecontract.Manifest{
		SchemaVersion: imagecontract.ManifestSchemaVersion,
		CreatedAt:     time.Now().UTC(),
		ToolVersion:   request.ToolVersion,
		Layout:        "hybrid-iso",
		Adapter:       AdapterID,
		SourceImage: imagecontract.ArtifactRecord{
			Path: "source.iso", SHA256: sourceDigest, Size: sourceInfo.Size(),
		},
		KernelBundle: portableKernelBundle(request.Bundle),
		BootArtifacts: imagecontract.BootArtifactRecord{
			Kernel: kernelRecord, Initrd: initrdRecord, DTBs: dtbs,
		},
		MediaDiscovery: imagecontract.MediaDiscoveryRecord{
			Strategy: "direct-hybrid-iso", Protocol: "dracut-live",
			Evidence: evidence,
		},
		CompanionBundle: companionRecord,
		BootArguments:   bootArguments,
		SecureBoot:      secureBootPolicy,
	}, nil
}

// writeSupportFiles assembles the immutable /sp11 tree mapped into the ISO.
func writeSupportFiles(workspace string, manifest imagecontract.Manifest, manifestBytes []byte, abi string) error {
	expected, err := serialiseManifest(manifest)
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, manifestBytes) {
		return errors.New("embedded manifest bytes differ from their manifest value")
	}
	sp11 := filepath.Join(workspace, "sp11")
	if err := os.MkdirAll(filepath.Join(sp11, "kernel"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(sp11, "fedora"), 0o755); err != nil {
		return err
	}
	for _, pkg := range manifest.KernelBundle.Packages {
		if pkg.Role != kernel.RoleImage && pkg.Role != kernel.RoleModules {
			continue
		}
		if err := stageFile(filepath.Join(workspace, "kernel", pkg.Name), filepath.Join(sp11, "kernel", pkg.Name)); err != nil {
			return err
		}
	}
	if err := stageFile(filepath.Join(workspace, "lexr-kernel-sp11.aarch64.rpm"), filepath.Join(sp11, "fedora", "lexr-kernel-sp11.aarch64.rpm")); err != nil {
		return err
	}
	_, iptsdIncluded, err := fedoraIPTSDUserspace(manifest.CompanionBundle)
	if err != nil {
		return err
	}
	if iptsdIncluded {
		if err := stageFile(filepath.Join(workspace, fedoraIPTSDRPMName), filepath.Join(sp11, "fedora", fedoraIPTSDRPMName)); err != nil {
			return err
		}
		if err := stageFile(filepath.Join(workspace, fedoraIPTSDSRPMName), filepath.Join(sp11, "fedora", fedoraIPTSDSRPMName)); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(sp11, "lexr-manifest.json"), manifestBytes, 0o644); err != nil {
		return err
	}
	if err := stageFile(filepath.Join(workspace, "fedora-support", "boot-policy.json"), filepath.Join(sp11, "fedora", "boot-policy.json")); err != nil {
		return err
	}
	companionNote := "No companion CLI or native IPTSD RPM was requested for this image."
	if manifest.CompanionBundle.Included {
		companionNote = "The Linux ARM64 Lexr companion and eligible offline userspace are under /sp11/companion. IPTSD remains offline-only unless that companion includes the exact source-bearing iptsd-v1 release."
		if iptsdIncluded {
			companionNote = "The Linux ARM64 Lexr companion and complete source-bearing IPTSD release are under /sp11/companion. The natively rebuilt, RPM-owned IPTSD runtime is installed in the live root; its binary and source RPMs are staged under /sp11/fedora. Do not run the portable IPTSD installer on Fedora because it would create an unowned /usr/local duplicate."
		}
	}
	readme := fmt.Sprintf("Lexr Fedora 44 for Surface Pro 11\n\nCustom kernel ABI: %s\n\nSecure Boot must be disabled. qcom_q6v5_pas is blacklisted only while the live root is on USB; Anaconda and the first installed boot remove that live-only policy. The Troubleshooting submenu has stock Fedora entries with explicit Surface device trees. v19 Stubble auto-DTB selection and installed-system hand-off are supported only for X1E/OLED. The X1P/LCD stock path is live-only; do not install from it. Proprietary firmware is not redistributed.\n\n%s\n", abi, companionNote)
	if err := os.WriteFile(filepath.Join(sp11, "README.txt"), []byte(readme), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workspace, "grub.cfg"), []byte(grubConfig(abi)), 0o644)
}

// validateBundlePaths rejects unsafe or incomplete host-side kernel bundle members.
func validateBundlePaths(bundle kernel.Bundle) error {
	if !safeKernelABIExpression.MatchString(bundle.ABI) {
		return fmt.Errorf("kernel ABI %q is not a safe path component", bundle.ABI)
	}
	if err := requireSupportedKernel(bundle.ABI); err != nil {
		return err
	}
	for _, role := range []kernel.PackageRole{kernel.RoleImage, kernel.RoleModules} {
		pkg, ok := bundle.Package(role)
		if !ok {
			return fmt.Errorf("kernel bundle has no %s package", role)
		}
		if !pkg.Verified {
			return fmt.Errorf("kernel package %s is not verified", pkg.Name)
		}
		if filepath.Base(pkg.Path) != pkg.Name {
			return fmt.Errorf("kernel package path does not match filename %s", pkg.Name)
		}
		info, err := os.Lstat(pkg.Path)
		if err != nil {
			return fmt.Errorf("inspect kernel package %s: %w", pkg.Name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("kernel package %s is not a non-symbolic-link regular file", pkg.Name)
		}
		if pkg.Size > 0 && info.Size() != pkg.Size {
			return fmt.Errorf("kernel package %s size mismatch", pkg.Name)
		}
		digest, err := artifact.HashFile(pkg.Path)
		if err != nil {
			return err
		}
		if !strings.EqualFold(digest, pkg.SHA256) {
			return fmt.Errorf("kernel package %s SHA-256 mismatch", pkg.Name)
		}
	}
	requiredDTBs := map[string]bool{
		"qcom/x1e80100-microsoft-denali-oled.dtb": false,
		"qcom/x1p64100-microsoft-denali.dtb":      false,
	}
	for _, dtb := range bundle.DeviceTrees {
		if _, ok := requiredDTBs[dtb.Path]; ok {
			requiredDTBs[dtb.Path] = true
		}
	}
	for path, found := range requiredDTBs {
		if !found {
			return fmt.Errorf("kernel bundle is missing Surface device tree %s", path)
		}
	}
	return nil
}

// stageBundle copies the verified kernel packages into the private build workspace.
func stageBundle(bundle kernel.Bundle, workspace string) error {
	directory := filepath.Join(workspace, "kernel")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	for _, pkg := range bundle.Packages {
		if pkg.Role != kernel.RoleImage && pkg.Role != kernel.RoleModules {
			continue
		}
		if err := stageFile(pkg.Path, filepath.Join(directory, pkg.Name)); err != nil {
			return err
		}
	}
	return nil
}

// stageFile snapshots a regular source without following a symbolic link.
func stageFile(source, destination string) (returnErr error) {
	listed, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect source %s: %w", source, err)
	}
	if listed.Mode()&os.ModeSymlink != 0 || !listed.Mode().IsRegular() {
		return fmt.Errorf("source %s is not a non-symbolic-link regular file", source)
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, input.Close()) }()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(listed, opened) {
		return errors.Join(fmt.Errorf("source identity changed while opening %s", source), err)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, listed.Mode().Perm())
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		returnErr = errors.Join(returnErr, output.Close())
		if !keep {
			returnErr = errors.Join(returnErr, os.Remove(destination))
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	keep = true
	return nil
}

// artifactRecord binds an embedded path to its host-side digest and size.
func artifactRecord(path, logicalPath string) (imagecontract.ArtifactRecord, error) {
	digest, err := artifact.HashFile(path)
	if err != nil {
		return imagecontract.ArtifactRecord{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return imagecontract.ArtifactRecord{}, err
	}
	record := imagecontract.ArtifactRecord{Path: filepath.ToSlash(logicalPath), SHA256: digest, Size: info.Size()}
	if err := imagecontract.ValidateArtifactRecord(record); err != nil {
		return imagecontract.ArtifactRecord{}, err
	}
	return record, nil
}

// portableKernelBundle rewrites host package paths to their ISO-relative locations.
func portableKernelBundle(bundle kernel.Bundle) kernel.Bundle {
	portable := bundle
	portable.Packages = nil
	for _, source := range bundle.Packages {
		if source.Role != kernel.RoleImage && source.Role != kernel.RoleModules {
			continue
		}
		pkg := source
		pkg.Path = "sp11/kernel/" + pkg.Name
		portable.Packages = append(portable.Packages, pkg)
	}
	portable.DeviceTrees = append([]kernel.DeviceTree(nil), bundle.DeviceTrees...)
	return portable
}

// serialiseManifest returns the canonical indented manifest bytes.
func serialiseManifest(manifest imagecontract.Manifest) ([]byte, error) {
	var buffer bytes.Buffer
	if err := manifest.WriteJSON(&buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// companionDigests returns journal inputs for an optional companion payload.
func companionDigests(record imagecontract.CompanionBundleRecord) map[string]string {
	digests := make(map[string]string)
	for _, record := range companion.FlattenArtifacts(record) {
		digests[record.Path] = record.SHA256
	}
	return digests
}

// packageDigests returns stable package-role keys for journal checkpoints.
func packageDigests(bundle kernel.Bundle) map[string]string {
	digests := make(map[string]string)
	for _, pkg := range bundle.Packages {
		if pkg.Role == kernel.RoleImage || pkg.Role == kernel.RoleModules {
			digests[pkg.Name] = pkg.SHA256
		}
	}
	return digests
}

// validPortableISOOutput validates the bounded destination basename contract.
func validPortableISOOutput(output string) bool {
	name := filepath.Base(filepath.Clean(output))
	return portableISONameExpression.MatchString(name) && strings.HasSuffix(strings.ToLower(name), ".iso") && !strings.ContainsAny(name, `/\`)
}

// samePath compares cleaned absolute paths without resolving unrelated symlinks.
func samePath(first, second string) bool {
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	if firstErr == nil && secondErr == nil {
		return os.SameFile(firstInfo, secondInfo)
	}
	return filepath.Clean(first) == filepath.Clean(second)
}

// logf emits one progress line when a writer is configured.
func logf(writer io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(writer, format+"\n", args...)
}

// sortedDTBPaths is kept small and deterministic for tests and diagnostics.
func sortedDTBPaths(bundle kernel.Bundle) []string {
	paths := make([]string, 0, len(bundle.DeviceTrees))
	for _, dtb := range bundle.DeviceTrees {
		paths = append(paths, dtb.Path)
	}
	sort.Strings(paths)
	return paths
}
