package archlinux

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/image/sp11"
	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// evidenceFiles binds every fixed boot/discovery payload to its on-media path.
var evidenceFiles = append([]struct{ role, file, path string }{
	{"live-filesystem", "airootfs.sfs", "arch/aarch64/airootfs.sfs"},
	{"live-checksum", "airootfs.sha512", "arch/aarch64/airootfs.sha512"},
	{"efi-bootloader", "BOOTAA64.EFI", "EFI/BOOT/BOOTAA64.EFI"},
	{"efi-system-partition", "esp.img", "boot/grub/efi.img"},
	{"grub-menu", "grub.cfg", "boot/grub/grub.cfg"},
	{"package-lock", "packages.lock.json", "sp11/packages.lock.json"},
	{"package-inventory", "packages.installed", "sp11/packages.installed"},
	{"native-kernel-package", "sp11/lexr-kernel-sp11.pkg.tar.gz", "sp11/lexr-kernel-sp11.pkg.tar.gz"},
	{"getting-started", "LEXR_GETTING_STARTED.txt", "LEXR_GETTING_STARTED.txt"},
}, installerEvidenceFiles()...)

// installerEvidenceFiles makes every offline installer input independently verifiable.
func installerEvidenceFiles() []struct{ role, file, path string } {
	var entries []struct{ role, file, path string }
	paths := append(installerAssetPaths()[1:], "installer/payload.json", "installer/guided.py", "installer/target.py")
	for index, name := range paths {
		path := "sp11/" + name
		entries = append(entries, struct{ role, file, path string }{fmt.Sprintf("installer-asset-%d", index), path, path})
	}
	return entries
}

// buildManifest records immutable Arch boot inputs and full kernel provenance.
func buildManifest(request Request, boot LiveBoot, workspace string, source imagecontract.ArtifactRecord, support imagecontract.CompanionBundleRecord) (imagecontract.Manifest, error) {
	m := imagecontract.Manifest{SchemaVersion: imagecontract.ManifestSchemaVersion, CreatedAt: time.Now().UTC(), ToolVersion: request.ToolVersion, Layout: "hybrid-iso", Adapter: AdapterID, SourceImage: source, KernelBundle: portableBundle(request.Bundle), CompanionBundle: support, SecureBoot: secureBootPolicy, BootArguments: strings.Fields(surfaceArguments)}
	var err error
	prefix := "arch/aarch64/boot/"
	m.BootArtifacts.Kernel, err = recordFile(filepath.Join(workspace, "vmlinuz"), prefix+"vmlinuz-"+boot.ABI)
	if err != nil {
		return m, err
	}
	m.BootArtifacts.Initrd, err = recordFile(filepath.Join(workspace, "live-initramfs.img"), prefix+"initramfs-"+boot.ABI+".img")
	if err != nil {
		return m, err
	}
	for _, tree := range request.Bundle.DeviceTrees {
		record, err := recordFile(filepath.Join(workspace, "dtbs", tree.Basename), prefix+"dtb-"+boot.ABI+"/"+tree.Basename)
		if err != nil {
			return m, err
		}
		if record.SHA256 != tree.SHA256 {
			return m, fmt.Errorf("Arch device tree %s differs from the kernel bundle", tree.Basename)
		}
		m.BootArtifacts.DTBs = append(m.BootArtifacts.DTBs, record)
	}
	label, _ := boot.Label()
	marker, _ := boot.Marker()
	m.MediaDiscovery = imagecontract.MediaDiscoveryRecord{Strategy: "direct-hybrid", Protocol: "archiso", Evidence: []imagecontract.MediaDiscoveryEvidence{{Role: "image-id", Scope: "iso-filesystem", Path: strings.TrimPrefix(marker, "/"), Value: boot.ImageID}, {Role: "volume-label", Scope: "iso9660", Value: label}}}
	for _, member := range evidenceFiles {
		record, err := recordFile(filepath.Join(workspace, member.file), member.path)
		if err != nil {
			return m, err
		}
		m.MediaDiscovery.Evidence = append(m.MediaDiscovery.Evidence, imagecontract.MediaDiscoveryEvidence{Role: member.role, Scope: "iso-filesystem", Path: member.path, Artifact: &record})
	}
	return m, nil
}

// validateManifest accepts only this adapter's complete and canonical paths.
func validateManifest(m imagecontract.Manifest) (LiveBoot, []imagecontract.ArtifactRecord, error) {
	boot := LiveBoot{ABI: m.KernelBundle.ABI}
	bad := func() (LiveBoot, []imagecontract.ArtifactRecord, error) {
		return boot, nil, errors.New("unsupported or incomplete Arch live manifest")
	}
	if m.SchemaVersion != imagecontract.ManifestSchemaVersion || m.Adapter != AdapterID || m.Layout != "hybrid-iso" || m.SecureBoot != secureBootPolicy || m.ToolVersion == "" || m.CreatedAt.IsZero() || m.KernelBundle.EffectiveDTBDelivery != kernel.DTBDeliveryExternalRequired {
		return bad()
	}
	if err := sp11.ValidateManifestBundle(m.KernelBundle); err != nil {
		return boot, nil, err
	}
	if err := imagecontract.ValidateArtifactRecord(m.SourceImage); err != nil {
		return boot, nil, err
	}
	if err := companion.ValidateRecord(m.CompanionBundle); err != nil {
		return boot, nil, err
	}
	if !reflect.DeepEqual(m.BootArguments, strings.Fields(surfaceArguments)) {
		return bad()
	}
	media := m.MediaDiscovery
	if media.Strategy != "direct-hybrid" || media.Protocol != "archiso" || len(media.Evidence) != len(evidenceFiles)+2 {
		return bad()
	}
	first := media.Evidence[0]
	boot.ImageID = first.Value
	if err := boot.Validate(); err != nil {
		return boot, nil, err
	}
	marker, _ := boot.Marker()
	label, _ := boot.Label()
	if first.Role != "image-id" || first.Scope != "iso-filesystem" || first.Path != strings.TrimPrefix(marker, "/") || first.Artifact != nil {
		return bad()
	}
	if media.Evidence[1] != (imagecontract.MediaDiscoveryEvidence{Role: "volume-label", Scope: "iso9660", Value: label}) {
		return bad()
	}
	records := []imagecontract.ArtifactRecord{m.BootArtifacts.Kernel, m.BootArtifacts.Initrd}
	for index, member := range evidenceFiles {
		e := media.Evidence[index+2]
		if e.Role != member.role || e.Scope != "iso-filesystem" || e.Path != member.path || e.Value != "" || e.Artifact == nil || e.Artifact.Path != member.path {
			return bad()
		}
		records = append(records, *e.Artifact)
	}
	prefix := "arch/aarch64/boot/"
	if m.BootArtifacts.Kernel.Path != prefix+"vmlinuz-"+boot.ABI || m.BootArtifacts.Initrd.Path != prefix+"initramfs-"+boot.ABI+".img" {
		return bad()
	}
	if len(m.BootArtifacts.DTBs) != 2 || len(m.KernelBundle.DeviceTrees) != 2 {
		return bad()
	}
	required := map[string]bool{"x1e80100-microsoft-denali-oled.dtb": false, "x1p64100-microsoft-denali.dtb": false}
	for index, tree := range m.KernelBundle.DeviceTrees {
		record := m.BootArtifacts.DTBs[index]
		seen, ok := required[tree.Basename]
		if !ok || seen {
			return bad()
		}
		required[tree.Basename] = true
		if record.Path != prefix+"dtb-"+boot.ABI+"/"+tree.Basename || record.SHA256 != tree.SHA256 {
			return bad()
		}
		records = append(records, record)
	}
	for _, pkg := range m.KernelBundle.Packages {
		if pkg.Path != "" {
			records = append(records, imagecontract.ArtifactRecord{Path: pkg.Path, SHA256: pkg.SHA256, Size: pkg.Size})
		}
	}
	if err := imagecontract.ValidateArtifactRecords(records); err != nil {
		return boot, nil, err
	}
	return boot, records, nil
}
