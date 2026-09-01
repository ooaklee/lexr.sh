package fedora

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// TestGrubConfigPreservesLiveDiscoveryAndFallback locks the directly written
// USB boot contract and the untouched Fedora recovery-kernel path together.
func TestGrubConfigPreservesLiveDiscoveryAndFallback(t *testing.T) {
	t.Parallel()

	abi := "6.17.0-sp11v19-qcom-x1e"
	config := grubConfig(abi)
	for _, required := range []string{
		"search --file --set=root /boot/0x503d6c7e",
		`set live_root="root=live:CDLABEL=` + SourceVolumeID + ` rd.live.image"`,
		`menuentry "Fedora 44 for Surface Pro 11 X1E/OLED (` + abi + `)"`,
		"linux ($root)/boot/aarch64/loader/linux quiet rhgb $live_root $sp11_args $usb_safe_args",
		"linux ($root)/boot/aarch64/loader/linux-fedora quiet rhgb $live_root $sp11_args $usb_safe_args",
		"devicetree ($root)/sp11/dtb/x1e80100-microsoft-denali-oled.dtb",
		"devicetree ($root)/sp11/dtb/x1p64100-microsoft-denali.dtb",
		"initrd ($root)/boot/aarch64/loader/initrd-fedora",
	} {
		if !strings.Contains(config, required) {
			t.Errorf("grubConfig() does not contain %q", required)
		}
	}
	if count := strings.Count(config, "menuentry "); count != 4 {
		t.Fatalf("menuentry count = %d, want 4", count)
	}
	linuxLines := 0
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "linux ") {
			continue
		}
		linuxLines++
		if !strings.Contains(line, "$live_root") || !strings.Contains(line, "$usb_safe_args") {
			t.Errorf("live kernel line lacks discovery or USB safety policy: %q", line)
		}
	}
	if linuxLines != 4 {
		t.Fatalf("live kernel line count = %d, want 4", linuxLines)
	}
	if count := strings.Count(config, "devicetree ($root)/sp11/dtb/"); count != 2 {
		t.Fatalf("stock fallback external DTB count = %d, want 2", count)
	}
}

// TestBootArgumentPolicySeparatesLiveAndInstalledSystems proves the ADSP
// blacklist remains USB-only while shared hardware arguments survive install.
func TestBootArgumentPolicySeparatesLiveAndInstalledSystems(t *testing.T) {
	t.Parallel()

	wantInstalled := []string{
		"clk_ignore_unused",
		"pd_ignore_unused",
		"systemd.tpm2_wait=0",
		"soundwire_qcom.sp11_feedback_active_offset2_zero=1",
	}
	wantLiveOnly := []string{
		"modprobe.blacklist=qcom_q6v5_pas",
		"rd.driver.blacklist=qcom_q6v5_pas",
	}
	if !slices.Equal(installedBootArguments, wantInstalled) {
		t.Fatalf("installedBootArguments = %q, want %q", installedBootArguments, wantInstalled)
	}
	if !slices.Equal(liveOnlyBootArguments, wantLiveOnly) {
		t.Fatalf("liveOnlyBootArguments = %q, want %q", liveOnlyBootArguments, wantLiveOnly)
	}

	liveConfig := grubConfig("6.17.0-sp11v19-qcom-x1e")
	installedDefaults := installedGrubDefaults()
	finalizer := installedFinalizeScript("6.17.0-sp11v19-qcom-x1e")
	for _, argument := range wantInstalled {
		for name, content := range map[string]string{"live GRUB": liveConfig, "installed GRUB": installedDefaults, "finalizer": finalizer} {
			if !strings.Contains(content, argument) {
				t.Errorf("%s omits installed argument %q", name, argument)
			}
		}
	}
	for _, argument := range wantLiveOnly {
		if !strings.Contains(liveConfig, argument) {
			t.Errorf("live GRUB omits USB-only argument %q", argument)
		}
		if strings.Contains(installedDefaults, argument) {
			t.Errorf("installed GRUB retains USB-only argument %q", argument)
		}
		if !strings.Contains(finalizer, argument) {
			t.Errorf("installed finalizer does not remove USB-only argument %q", argument)
		}
	}
}

// TestRPMVersionProducesPortableFedoraVersions covers Debian punctuation and
// degenerate inputs before they enter an RPM header or changelog.
func TestRPMVersionProducesPortableFedoraVersions(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		"6.17.0":               "6.17.0",
		"6.17.0-rc1+sp11~test": "6.17.0.rc1+sp11~test",
		"..alpha--beta..":      "alpha.beta",
		"---":                  "1",
		"":                     "1",
	} {
		if got := rpmVersion(input); got != want {
			t.Errorf("rpmVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestKernelRPMSpecOwnsTheExactKernelLifecycle verifies the generated package
// advertises, installs, registers, and removes one immutable custom ABI.
func TestKernelRPMSpecOwnsTheExactKernelLifecycle(t *testing.T) {
	t.Parallel()

	abi := "6.17.0-sp11v19-qcom-x1e"
	spec := kernelRPMSpec(abi, "6.17.0-rc1+sp11")
	for _, required := range []string{
		"Name:           " + fedoraKernelPackageName,
		"Version:        6.17.0.rc1+sp11",
		"BuildArch:      aarch64",
		"Provides:       kernel-uname-r",
		"Provides:       kernel-core-uname-r",
		"Provides:       kernel-modules-core-uname-r",
		"Provides:       installonlypkg(kernel)",
		"Requires:       /usr/bin/dracut",
		"/boot/vmlinuz-" + abi,
		"/usr/lib/modules/" + abi,
		"/usr/lib/firmware/" + abi + "/device-tree",
		"/etc/kernel/install.conf",
		"/usr/sbin/depmod -a " + abi + " || exit 1",
		"/usr/bin/dracut --force /boot/initramfs-" + abi + ".img " + abi + " || exit 1",
		"KERNEL_INSTALL_LAYOUT=other /usr/bin/kernel-install add " + abi + " /usr/lib/modules/" + abi + "/vmlinuz-dtbloader.efi || exit 1",
		"KERNEL_INSTALL_LAYOUT=other /usr/bin/kernel-install remove " + abi + " || :",
	} {
		if !strings.Contains(spec, required) {
			t.Errorf("kernelRPMSpec() does not contain %q", required)
		}
	}
	if strings.Contains(spec, "%!") {
		t.Fatalf("kernelRPMSpec() contains an unresolved formatting operand:\n%s", spec)
	}
	if strings.Contains(spec, "kernel-uname-r = "+abi) {
		t.Fatalf("kernelRPMSpec() emits an RPM 6-invalid multi-hyphen uname EVR:\n%s", spec)
	}
	if got := kernelInstallConfiguration(); got != "layout=other\n" {
		t.Fatalf("kernelInstallConfiguration() = %q, want layout=other", got)
	}
}

// TestInstalledFinalizerIsGuardedAndRebuildsTheExactABI verifies the one-shot
// hand-off removes live policy before rebuilding persistent boot artefacts.
func TestInstalledFinalizerIsGuardedAndRebuildsTheExactABI(t *testing.T) {
	t.Parallel()

	abi := "6.17.0-sp11v19-qcom-x1e"
	script := installedFinalizeScript(abi)
	guard := `[ ! -e "$state" ] || exit 0`
	removeDenylist := "rm -f -- /etc/modprobe.d/anaconda-denylist.conf"
	finish := `: > "$state"`
	for _, required := range []string{
		`abi="` + abi + `"`,
		guard,
		removeDenylist,
		`grubby --update-kernel="/boot/vmlinuz-$abi"`,
		`grubby --set-default="/boot/vmlinuz-$abi"`,
		`--remove-args="modprobe.blacklist=qcom_q6v5_pas rd.driver.blacklist=qcom_q6v5_pas"`,
		`/usr/sbin/depmod -a "$abi"`,
		`/usr/bin/dracut --force "/boot/initramfs-$abi.img" "$abi"`,
		`KERNEL_INSTALL_LAYOUT=other /usr/bin/kernel-install add "$abi"`,
		`rpm -q --whatprovides "$boot_image"`,
		`install -m 0755 "$stock_image" "$boot_image"`,
		`/usr/sbin/depmod -a "$stock_abi"`,
		`/usr/bin/dracut --force "/boot/initramfs-$stock_abi.img" "$stock_abi"`,
		`fallback_dtb=qcom/x1e80100-microsoft-denali-oled.dtb`,
		`install -D -m 0644 "$fallback_dtb_source" "$stock_dtb"`,
		`GRUB_DEVICETREE="$fallback_dtb" KERNEL_INSTALL_LAYOUT=other`,
		`/usr/bin/kernel-install add "$stock_abi" "$stock_image"`,
		`cmp "$fallback_dtb_source" "$stock_dtb"`,
		`grep -Fx "devicetree /dtb-$stock_abi/$fallback_dtb" "$bls_entry"`,
		finish,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("installedFinalizeScript() does not contain %q", required)
		}
	}
	if !(strings.Index(script, guard) < strings.Index(script, removeDenylist) &&
		strings.Index(script, removeDenylist) < strings.Index(script, finish)) {
		t.Fatalf("installed finalizer guard/cleanup/completion ordering changed:\n%s", script)
	}
	stockInstall := strings.Index(script, `install -m 0755 "$stock_image" "$boot_image"`)
	stockDepmod := strings.Index(script, `/usr/sbin/depmod -a "$stock_abi"`)
	stockDracut := strings.Index(script, `/usr/bin/dracut --force "/boot/initramfs-$stock_abi.img" "$stock_abi"`)
	stockRegister := strings.Index(script, `/usr/bin/kernel-install add "$stock_abi" "$stock_image"`)
	if !(stockInstall < stockDepmod && stockDepmod < stockDracut && stockDracut < stockRegister) {
		t.Fatalf("stock fallback install/depmod/dracut/kernel-install ordering changed:\n%s", script)
	}

	service := installedFinalizeService()
	for _, required := range []string{
		"ConditionKernelCommandLine=!rd.live.image",
		"ConditionPathExists=!/var/lib/lexr/sp11-installed-finalized",
		"ExecStart=/usr/lib/lexr/sp11/finalize-installed",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(service, required) {
			t.Errorf("installedFinalizeService() does not contain %q", required)
		}
	}
}

// TestFedoraMediaDiscoveryRequiresLabelAndLiveRootEvidence rejects manifests
// that describe only one side of the dracut-live discovery agreement.
func TestFedoraMediaDiscoveryRequiresLabelAndLiveRootEvidence(t *testing.T) {
	t.Parallel()

	valid := imagecontract.MediaDiscoveryRecord{
		Strategy: "direct-hybrid-iso",
		Protocol: "dracut-live",
		Evidence: []imagecontract.MediaDiscoveryEvidence{
			{Role: "iso-volume-label", Scope: "iso9660-pvd", Value: SourceVolumeID},
			{Role: "live-root", Scope: "grub", Value: "root=live:CDLABEL=" + SourceVolumeID + " rd.live.image"},
		},
	}
	if !validFedoraMediaDiscovery(valid) {
		t.Fatal("validFedoraMediaDiscovery() rejected the complete Fedora discovery record")
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*imagecontract.MediaDiscoveryRecord)
	}{
		{name: "strategy", mutate: func(record *imagecontract.MediaDiscoveryRecord) { record.Strategy = "partition-copy" }},
		{name: "protocol", mutate: func(record *imagecontract.MediaDiscoveryRecord) { record.Protocol = "casper" }},
		{name: "missing label", mutate: func(record *imagecontract.MediaDiscoveryRecord) { record.Evidence = record.Evidence[1:] }},
		{name: "wrong live root", mutate: func(record *imagecontract.MediaDiscoveryRecord) { record.Evidence[1].Value += " rd.live.check" }},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			record := valid
			record.Evidence = append([]imagecontract.MediaDiscoveryEvidence(nil), valid.Evidence...)
			testCase.mutate(&record)
			if validFedoraMediaDiscovery(record) {
				t.Fatalf("validFedoraMediaDiscovery() accepted %#v", record)
			}
		})
	}
}

// TestFindEvidenceArtifactRequiresMatchingPortablePaths prevents a manifest
// evidence wrapper from rebinding a native RPM record to another ISO member.
func TestFindEvidenceArtifactRequiresMatchingPortablePaths(t *testing.T) {
	t.Parallel()

	artifact := imagecontract.ArtifactRecord{
		Path:   "sp11/fedora/lexr-kernel-sp11.aarch64.rpm",
		SHA256: strings.Repeat("a", 64), Size: 42,
	}
	record := imagecontract.MediaDiscoveryRecord{Evidence: []imagecontract.MediaDiscoveryEvidence{{
		Role: "installed-kernel-rpm", Scope: "iso9660", Path: artifact.Path, Artifact: &artifact,
	}}}
	got, ok := findEvidenceArtifact(record, "installed-kernel-rpm")
	if !ok || got != artifact {
		t.Fatalf("findEvidenceArtifact() = %#v, %t", got, ok)
	}
	record.Evidence[0].Path = "sp11/fedora/other.rpm"
	if _, ok := findEvidenceArtifact(record, "installed-kernel-rpm"); ok {
		t.Fatal("findEvidenceArtifact() accepted mismatched evidence and artefact paths")
	}
}

// TestBootPolicyDecoderRejectsUnknownFields locks the strict policy JSON
// boundary used after extracting the independently embedded support record.
func TestBootPolicyDecoderRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(expectedBootPolicy("6.17.0-sp11v19-qcom-x1e"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeBootPolicy(data); err != nil {
		t.Fatalf("decodeBootPolicy() error = %v", err)
	}
	data = []byte(strings.Replace(string(data), `{`, `{"unexpected":true,`, 1))
	if _, err := decodeBootPolicy(data); err == nil {
		t.Fatal("decodeBootPolicy() accepted an unknown field")
	}
}

// TestFedoraManifestRequiresCompleteProvenanceAndPolicy rejects incomplete
// package, hardware, source, boot-policy, and native-RPM declarations.
func TestFedoraManifestRequiresCompleteProvenanceAndPolicy(t *testing.T) {
	t.Parallel()

	valid := completeFedoraManifestFixture()
	if err := validateFedoraManifest(valid); err != nil {
		t.Fatalf("validateFedoraManifest() error = %v", err)
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*imagecontract.Manifest)
	}{
		{name: "packages", mutate: func(manifest *imagecontract.Manifest) { manifest.KernelBundle.Packages = nil }},
		{name: "device trees", mutate: func(manifest *imagecontract.Manifest) { manifest.KernelBundle.DeviceTrees = nil }},
		{name: "source", mutate: func(manifest *imagecontract.Manifest) { manifest.SourceImage.Path = "elsewhere.iso" }},
		{name: "boot arguments", mutate: func(manifest *imagecontract.Manifest) { manifest.BootArguments = nil }},
		{name: "secure boot", mutate: func(manifest *imagecontract.Manifest) { manifest.SecureBoot = "supported" }},
		{name: "native RPM", mutate: func(manifest *imagecontract.Manifest) {
			manifest.MediaDiscovery.Evidence = manifest.MediaDiscovery.Evidence[:2]
		}},
		{name: "stock fallback kernel", mutate: func(manifest *imagecontract.Manifest) {
			manifest.MediaDiscovery.Evidence = append(manifest.MediaDiscovery.Evidence[:3], manifest.MediaDiscovery.Evidence[4:]...)
		}},
		{name: "stock fallback initramfs", mutate: func(manifest *imagecontract.Manifest) {
			manifest.MediaDiscovery.Evidence = manifest.MediaDiscovery.Evidence[:4]
		}},
		{name: "unbacked IPTSD RPM", mutate: func(manifest *imagecontract.Manifest) {
			record := imagecontract.ArtifactRecord{Path: fedoraIPTSDRPMPath, SHA256: strings.Repeat("d", 64), Size: 42}
			manifest.MediaDiscovery.Evidence = append(manifest.MediaDiscovery.Evidence, imagecontract.MediaDiscoveryEvidence{
				Role: "installed-iptsd-rpm", Scope: "iso9660", Path: record.Path, Artifact: &record,
			})
		}},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			manifest := valid
			manifest.KernelBundle.Packages = append([]kernel.Package(nil), valid.KernelBundle.Packages...)
			manifest.KernelBundle.DeviceTrees = append([]kernel.DeviceTree(nil), valid.KernelBundle.DeviceTrees...)
			manifest.BootArguments = append([]string(nil), valid.BootArguments...)
			manifest.MediaDiscovery.Evidence = append([]imagecontract.MediaDiscoveryEvidence(nil), valid.MediaDiscovery.Evidence...)
			testCase.mutate(&manifest)
			if err := validateFedoraManifest(manifest); err == nil {
				t.Fatalf("validateFedoraManifest() accepted the %s mutation", testCase.name)
			}
		})
	}
}

// completeFedoraManifestFixture supplies the exact adapter contract for focused mutations.
func completeFedoraManifestFixture() imagecontract.Manifest {
	abi := "6.17.0-sp11v19-qcom-x1e"
	version := "6.17.0"
	record := func(path string) imagecontract.ArtifactRecord {
		return imagecontract.ArtifactRecord{Path: path, SHA256: strings.Repeat("a", 64), Size: 42}
	}
	rpmRecord := record("sp11/fedora/lexr-kernel-sp11.aarch64.rpm")
	stockKernelRecord := record("boot/aarch64/loader/linux-fedora")
	stockInitrdRecord := record("boot/aarch64/loader/initrd-fedora")
	arguments := append([]string(nil), installedBootArguments...)
	arguments = append(arguments, liveOnlyBootArguments...)
	return imagecontract.Manifest{
		SchemaVersion: imagecontract.ManifestSchemaVersion,
		CreatedAt:     time.Unix(1, 0).UTC(),
		ToolVersion:   "test",
		Layout:        "hybrid-iso",
		Adapter:       AdapterID,
		SourceImage:   record("source.iso"),
		KernelBundle: kernel.Bundle{
			SchemaVersion: kernel.BundleSchemaVersion,
			Release:       "sp11-test",
			ABI:           abi,
			Version:       version,
			Architecture:  "arm64",
			Packages: []kernel.Package{
				{Role: kernel.RoleImage, Name: "linux-image-" + abi + "_" + version + "_arm64.deb", Path: "sp11/kernel/linux-image-" + abi + "_" + version + "_arm64.deb", SHA256: strings.Repeat("b", 64), Size: 11, Verified: true},
				{Role: kernel.RoleModules, Name: "linux-modules-" + abi + "_" + version + "_arm64.deb", Path: "sp11/kernel/linux-modules-" + abi + "_" + version + "_arm64.deb", SHA256: strings.Repeat("c", 64), Size: 12, Verified: true},
			},
			DeviceTrees: []kernel.DeviceTree{
				{Device: "surface-pro-11-x1e-oled", Path: "qcom/x1e80100-microsoft-denali-oled.dtb"},
				{Device: "surface-pro-11-x1p-lcd", Path: "qcom/x1p64100-microsoft-denali.dtb"},
			},
		},
		BootArtifacts: imagecontract.BootArtifactRecord{
			Kernel: record("boot/aarch64/loader/linux"),
			Initrd: record("boot/aarch64/loader/initrd"),
			DTBs: []imagecontract.ArtifactRecord{
				record("sp11/dtb/x1e80100-microsoft-denali-oled.dtb"),
				record("sp11/dtb/x1p64100-microsoft-denali.dtb"),
			},
		},
		MediaDiscovery: imagecontract.MediaDiscoveryRecord{
			Strategy: "direct-hybrid-iso", Protocol: "dracut-live",
			Evidence: []imagecontract.MediaDiscoveryEvidence{
				{Role: "iso-volume-label", Scope: "iso9660-pvd", Value: SourceVolumeID},
				{Role: "live-root", Scope: "grub", Value: "root=live:CDLABEL=" + SourceVolumeID + " rd.live.image"},
				{Role: "installed-kernel-rpm", Scope: "iso9660", Path: rpmRecord.Path, Artifact: &rpmRecord},
				{Role: "stock-fallback-kernel", Scope: "iso9660", Path: stockKernelRecord.Path, Artifact: &stockKernelRecord},
				{Role: "stock-fallback-initramfs", Scope: "iso9660", Path: stockInitrdRecord.Path, Artifact: &stockInitrdRecord},
			},
		},
		CompanionBundle: companion.Absent(companion.OmissionReasonNotRequested),
		BootArguments:   arguments,
		SecureBoot:      secureBootPolicy,
	}
}

// TestAppendedESPOffsetParsesOnlyBoundedGPTExtents covers xorriso's sector
// report, missing partitions, empty partitions, and byte-offset overflow.
func TestAppendedESPOffsetParsesOnlyBoundedGPTExtents(t *testing.T) {
	t.Parallel()

	offset, err := appendedESPOffset("GPT start and size : 2 123 45")
	if err != nil {
		t.Fatal(err)
	}
	if offset != 123*512 {
		t.Fatalf("appendedESPOffset() = %d, want %d", offset, 123*512)
	}
	for _, report := range []string{
		"no partition report",
		"GPT start and size : 2 123 0",
		"GPT start and size : 2 18014398509481984 1",
	} {
		if _, err := appendedESPOffset(report); err == nil {
			t.Errorf("appendedESPOffset(%q) unexpectedly succeeded", report)
		}
	}
}

// TestPortableKernelBundleRewritesPathsWithoutAliasing preserves the original
// verified host bundle while producing deterministic on-media package paths.
func TestPortableKernelBundleRewritesPathsWithoutAliasing(t *testing.T) {
	t.Parallel()

	bundle := kernel.Bundle{
		Packages: []kernel.Package{
			{Role: kernel.RoleImage, Name: "linux-image.deb", Path: "/host/linux-image.deb"},
			{Role: kernel.RoleModules, Name: "linux-modules.deb", Path: "/host/linux-modules.deb"},
			{Role: kernel.RoleHeaders, Name: "linux-headers.deb", Path: "/host/linux-headers.deb"},
		},
		DeviceTrees: []kernel.DeviceTree{
			{Device: "x1p", Path: "qcom/x1p.dtb"},
			{Device: "x1e", Path: "qcom/x1e.dtb"},
		},
	}
	portable := portableKernelBundle(bundle)
	if len(portable.Packages) != 2 || portable.Packages[0].Path != "sp11/kernel/linux-image.deb" ||
		portable.Packages[1].Path != "sp11/kernel/linux-modules.deb" {
		t.Fatalf("portable package paths = %#v", portable.Packages)
	}
	portable.Packages[0].Path = "mutated"
	portable.DeviceTrees[0].Path = "mutated"
	if bundle.Packages[0].Path != "/host/linux-image.deb" || bundle.DeviceTrees[0].Path != "qcom/x1p.dtb" {
		t.Fatal("portableKernelBundle() aliases mutable input slices")
	}
	if got, want := sortedDTBPaths(bundle), []string{"qcom/x1e.dtb", "qcom/x1p.dtb"}; !slices.Equal(got, want) {
		t.Fatalf("sortedDTBPaths() = %q, want %q", got, want)
	}
}

// TestEROFSExtractionForcesRootTraversal locks the Fedora 44 packed-fragment
// workaround and the metadata-preserving extraction contract used by both the
// builder and independent validator.
func TestEROFSExtractionForcesRootTraversal(t *testing.T) {
	t.Parallel()

	got := erofsExtractionArguments("/work/live.erofs", "/linux-work/rootfs")
	want := []string{
		"fsck.erofs", "--extract=/linux-work/rootfs", "--path=/",
		"--xattrs", "--preserve", "/work/live.erofs",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("erofsExtractionArguments() = %q, want %q", got, want)
	}
}

// TestValidPortableISOOutputRestrictsThePublishedBasename covers case-insensitive
// ISO suffixes while rejecting traversal-like or non-portable filenames.
func TestValidPortableISOOutputRestrictsThePublishedBasename(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{
		"fedora-surface.iso":       true,
		"Fedora_Surface.ISO":       true,
		"release/fedora+sp11.iso":  true,
		"fedora.iso.manifest.json": false,
		"fedora surface.iso":       false,
		".iso":                     false,
	} {
		if got := validPortableISOOutput(name); got != want {
			t.Errorf("validPortableISOOutput(%q) = %t, want %t", name, got, want)
		}
	}
}
