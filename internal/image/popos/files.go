package popos

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Pop's media-check format requires MD5.
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/caspermedia"
	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// maximumISOBytes bounds private copies before image tooling reads them.
const maximumISOBytes int64 = 64 << 30

// recordFile binds a generated regular file to its portable on-media path.
func recordFile(file, mediaPath string) (imagecontract.ArtifactRecord, error) {
	info, err := os.Lstat(file)
	if err != nil {
		return imagecontract.ArtifactRecord{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return imagecontract.ArtifactRecord{}, fmt.Errorf("generated artefact %s is not a non-empty regular file", mediaPath)
	}
	digest, err := artifact.HashFile(file)
	if err != nil {
		return imagecontract.ArtifactRecord{}, err
	}
	return imagecontract.ArtifactRecord{Path: mediaPath, SHA256: digest, Size: info.Size()}, nil
}

// stageKernelBundle copies only the runtime package set into the private
// workspace, checking each copy against the already verified bundle identity.
func stageKernelBundle(ctx context.Context, bundle kernel.Bundle, workspace string) error {
	if err := os.MkdirAll(filepath.Join(workspace, "kernel"), 0o755); err != nil {
		return err
	}
	for _, pkg := range bundle.Packages {
		if pkg.Role != kernel.RoleImage && pkg.Role != kernel.RoleModules && pkg.Role != kernel.RoleBootSupport {
			continue
		}
		digest, size, err := imagecontract.SnapshotFile(ctx, pkg.Path, filepath.Join(workspace, "kernel", pkg.Name), maximumISOBytes, nil)
		if err != nil {
			return err
		}
		if digest != pkg.SHA256 || size != pkg.Size {
			return fmt.Errorf("kernel package %s changed during staging", pkg.Name)
		}
	}
	return nil
}

// portableBundle strips local paths from the self-contained media manifest.
func portableBundle(bundle kernel.Bundle) kernel.Bundle {
	result := bundle
	result.Packages = append([]kernel.Package(nil), bundle.Packages...)
	result.DeviceTrees = kernel.CloneDeviceTrees(bundle.DeviceTrees)
	for index := range result.Packages {
		pkg := &result.Packages[index]
		pkg.Path = ""
		if pkg.Role == kernel.RoleImage || pkg.Role == kernel.RoleModules || pkg.Role == kernel.RoleBootSupport {
			pkg.Path = "sp11/kernel/" + pkg.Name
		}
	}
	return result
}

// buildManifest records Casper identity, installer identity and both EFI boot
// paths alongside the common kernel, device-tree and companion contracts.
func buildManifest(request Request, layout sourceLayout, workspace string, source imagecontract.ArtifactRecord, companion imagecontract.CompanionBundleRecord) (imagecontract.Manifest, error) {
	manifest := imagecontract.Manifest{
		SchemaVersion: imagecontract.ManifestSchemaVersion, CreatedAt: time.Now().UTC(),
		ToolVersion: request.ToolVersion, Layout: "hybrid-iso", Adapter: AdapterID,
		SourceImage: source, KernelBundle: portableBundle(request.Bundle),
		CompanionBundle: companion, SecureBoot: secureBootPolicy,
		BootArguments: strings.Fields("boot=casper live-media-path=/" + layout.liveDirectory + " " + surfaceKernelArguments),
	}
	var err error
	manifest.BootArtifacts.Kernel, err = recordFile(filepath.Join(workspace, "casper-vmlinuz"), layout.kernel)
	if err != nil {
		return manifest, err
	}
	manifest.BootArtifacts.Initrd, err = recordFile(filepath.Join(workspace, "casper-initrd"), layout.initrd)
	if err != nil {
		return manifest, err
	}
	for _, tree := range request.Bundle.DeviceTrees {
		mediaPath := "sp11/dtb/" + tree.Basename
		record, err := recordFile(filepath.Join(workspace, filepath.FromSlash(mediaPath)), mediaPath)
		if err != nil {
			return manifest, err
		}
		if record.SHA256 != tree.SHA256 {
			return manifest, fmt.Errorf("staged device tree %s differs from its kernel bundle", tree.Basename)
		}
		manifest.BootArtifacts.DTBs = append(manifest.BootArtifacts.DTBs, record)
	}
	uuid, err := imagecontract.ReadBoundedExtractedFile(workspace, "casper-uuid-generic", 64)
	if err != nil {
		return manifest, err
	}
	contract, err := caspermedia.NewDirectHybrid(uuid)
	if err != nil {
		return manifest, err
	}
	marker, err := recordFile(filepath.Join(workspace, "casper-uuid-generic"), caspermedia.MediumIdentityPath)
	if err != nil {
		return manifest, err
	}
	manifest.MediaDiscovery, err = contract.DiscoveryRecord(marker)
	if err != nil {
		return manifest, err
	}
	manifest.MediaDiscovery.Evidence = append(manifest.MediaDiscovery.Evidence, imagecontract.MediaDiscoveryEvidence{
		Role: "live-directory", Scope: "iso-filesystem", Path: layout.liveDirectory, Value: "/" + layout.liveDirectory,
	})
	for _, member := range []struct{ role, file, mediaPath string }{
		{"efi-bootloader", "grubaa64.efi", "efi/boot/bootaa64.efi"},
		{"efi-system-partition", "esp.img", "boot/grub/efi.img"},
		{"installer-product", "disk-info", ".disk/info"},
	} {
		record, err := recordFile(filepath.Join(workspace, member.file), member.mediaPath)
		if err != nil {
			return manifest, err
		}
		manifest.MediaDiscovery.Evidence = append(manifest.MediaDiscovery.Evidence, imagecontract.MediaDiscoveryEvidence{
			Role: member.role, Scope: "iso-filesystem", Path: member.mediaPath, Artifact: &record,
		})
	}
	return manifest, nil
}

// manifestBytes serialises one bounded manifest for identical on-media and
// sidecar publication, rejecting incomplete or oversized output.
func manifestBytes(manifest imagecontract.Manifest) ([]byte, error) {
	var output bytes.Buffer
	if err := manifest.WriteJSON(&output); err != nil {
		return nil, err
	}
	if output.Len() == 0 || output.Len() > imagecontract.MaximumManifestSize {
		return nil, errors.New("generated Pop manifest exceeds its size bound")
	}
	return output.Bytes(), nil
}

// updateMediaChecksums preserves the source inventory and replaces changed
// members. MD5 is only Pop's legacy media-check format; SHA-256 binds all Lexr
// provenance and publication records independently.
func updateMediaChecksums(workspace string, replacements map[string]string) error {
	data, err := imagecontract.ReadBoundedExtractedFile(workspace, "source-md5sum.txt", 4<<20)
	if err != nil {
		return err
	}
	entries := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 32 || !strings.HasPrefix(fields[1], "./") || !safeChecksumMember(strings.TrimPrefix(fields[1], "./")) {
			return errors.New("unsupported Pop MD5 inventory line")
		}
		if strings.Trim(fields[0], "0123456789abcdef") != "" {
			return errors.New("invalid source media-check digest")
		}
		if _, exists := entries[fields[1]]; exists {
			return errors.New("duplicate source media-check member")
		}
		entries[fields[1]] = fields[0]
	}
	for member, file := range replacements {
		if !safeChecksumMember(member) {
			return fmt.Errorf("unsafe replacement media-check member %q", member)
		}
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		hash := md5.New() //nolint:gosec // Required for Pop's md5sum.txt.
		_, readErr := io.Copy(hash, input)
		if err := errors.Join(readErr, input.Close()); err != nil {
			return err
		}
		entries["./"+member] = fmt.Sprintf("%x", hash.Sum(nil))
	}
	keys := make([]string, 0, len(entries))
	for member := range entries {
		keys = append(keys, member)
	}
	sort.Strings(keys)
	var output strings.Builder
	for _, member := range keys {
		fmt.Fprintf(&output, "%s  %s\n", entries[member], member)
	}
	return os.WriteFile(filepath.Join(workspace, "md5sum.txt"), []byte(output.String()), 0o644)
}

// safeChecksumMember accepts only canonical relative ISO member names that the
// whitespace-delimited source media-check inventory can represent unambiguously.
func safeChecksumMember(member string) bool {
	return member != "" && member != "." && member != ".." &&
		!strings.HasPrefix(member, "/") && !strings.HasPrefix(member, "../") &&
		path.Clean(member) == member && !strings.ContainsAny(member, "\\\x00\r\n\t ")
}
