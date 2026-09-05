package popos

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
)

// recoveryTestSerial is a native FAT filesystem serial, not an RFC UUID.
const recoveryTestSerial = "FEED-0123"

// recoveryTestPartUUID identifies the fixture recovery GPT partition.
const recoveryTestPartUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

// makeRecoveryFixture models the files Distinst actually copies and writes,
// using the production Go manifest schema for the original-media record.
func makeRecoveryFixture(t *testing.T) popFixture {
	t.Helper()
	fixture := writePopFixture(t, true)
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := imagecontract.Manifest{SchemaVersion: imagecontract.ManifestSchemaVersion, Adapter: AdapterID, KernelBundle: popTestBundle(), CompanionBundle: companion.Absent(companion.OmissionReasonNotRequested)}
	dtb := "original-live-dtb"
	manifest.KernelBundle.DeviceTrees[0].SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(dtb)))
	tree := manifest.KernelBundle.DeviceTrees[0]
	manifest.BootArtifacts.DTBs = []imagecontract.ArtifactRecord{{Path: "sp11/dtb/" + tree.Basename, SHA256: tree.SHA256, Size: int64(len(dtb))}}
	for role, text := range map[string]string{"kernel": "original-live-kernel", "initrd": "original-live-initrd"} {
		name := "vmlinuz.efi"
		if role == "initrd" {
			name = "initrd.gz"
		}
		record := imagecontract.ArtifactRecord{Path: "casper_pop-os_fixture/" + name, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(text))), Size: int64(len(text))}
		if role == "kernel" {
			manifest.BootArtifacts.Kernel = record
		} else {
			manifest.BootArtifacts.Initrd = record
		}
		write(filepath.Join(fixture.root, "recovery", "casper-"+recoveryTestSerial, name), text)
		write(filepath.Join(fixture.esp, "EFI", "Recovery-"+recoveryTestSerial, name), text)
	}
	manifest.MediaDiscovery.Evidence = []imagecontract.MediaDiscoveryEvidence{{Role: "medium-identity", Value: popTestRootUUID}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(fixture.root, "usr/share/lexr/pop-media/lexr-manifest.json"), string(data))
	write(filepath.Join(fixture.root, "usr/share/lexr/pop-media/sp11/dtb", tree.Basename), dtb)
	write(filepath.Join(fixture.root, "recovery/.disk/casper-uuid-generic"), popTestRootUUID+"\n")
	write(filepath.Join(fixture.root, "recovery/recovery.conf"), "ROOT_UUID="+popTestRootUUID+"\nRECOVERY_UUID=PARTUUID="+recoveryTestPartUUID+"\n")
	write(filepath.Join(fixture.esp, "loader/entries/Recovery-"+recoveryTestSerial+".conf"),
		"title Pop!_OS recovery\nlinux /EFI/Recovery-"+recoveryTestSerial+"/vmlinuz.efi\ninitrd /EFI/Recovery-"+recoveryTestSerial+"/initrd.gz\noptions boot=casper hostname=recovery userfullname=Recovery username=recovery live-media-path=/casper-"+recoveryTestSerial+" live-media=/dev/disk/by-partuuid/"+recoveryTestPartUUID+" noprompt\n")
	mounts := fmt.Sprintf("/dev/vda1 %s ext4 rw 0 0\n/dev/vda2 %s vfat rw 0 0\n/dev/vda3 %s/recovery vfat rw 0 0\n", fixture.root, fixture.esp, fixture.root)
	write(filepath.Join(fixture.root, "proc/mounts"), mounts)
	return fixture
}

// runRecoveryFixture executes the maintained helper once. Only block-device
// identity is mocked: macOS cannot create Linux block nodes. File contents,
// mount records, DTB pairing, publication and ownership checks run unchanged.
func runRecoveryFixture(t *testing.T, fixture popFixture) (string, int) {
	t.Helper()
	directory := t.TempDir()
	for name, source := range map[string]string{"pop-boot-refresh": popBootRefreshHelper, "pop-recovery-refresh": popRecoveryRefresh} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(source), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	const driver = `import importlib.machinery, importlib.util, os, sys
loader = importlib.machinery.SourceFileLoader("recovery", sys.argv[1])
spec = importlib.util.spec_from_loader(loader.name, loader)
module = importlib.util.module_from_spec(spec)
loader.exec_module(module)
def device(path):
    if path == "/dev/vda2" or path.endswith("/dev/disk/by-uuid/ABCD-1234"):
        return (254, 2)
    if path == "/dev/vda3" or path.endswith("/dev/disk/by-partuuid/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"):
        return (254, 3)
    return None
module.boot.device_identity = device
sys.argv = [sys.argv[1], "--root", sys.argv[2], "--abi", sys.argv[3]]
sys.exit(module.main())
`
	command := exec.Command(popPython(t), "-c", driver, filepath.Join(directory, "pop-recovery-refresh"), fixture.root, popInstalledTestABI)
	output, err := command.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	if failed, ok := err.(*exec.ExitError); ok {
		return string(output), failed.ExitCode()
	}
	t.Fatalf("execute recovery fixture: %v", err)
	return "", -1
}

// TestPopRecoveryUsesOriginalMediaDTBAfterKernelUpdates verifies that recovery
// retains its original kernel/DTB while the installed kernel evolves.
func TestPopRecoveryUsesOriginalMediaDTBAfterKernelUpdates(t *testing.T) {
	fixture := makeRecoveryFixture(t)
	native := filepath.Join(fixture.esp, "loader/entries/Recovery-"+recoveryTestSerial+".conf")
	before, err := os.ReadFile(native)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		output, status := runRecoveryFixture(t, fixture)
		if status != 0 {
			t.Fatalf("recovery status %d: %s", status, output)
		}
	}
	entry, err := os.ReadFile(filepath.Join(fixture.esp, "loader/entries/lexr-recovery-"+popTestRootUUID+"-"+recoveryTestSerial+".conf"))
	if err != nil {
		t.Fatal(err)
	}
	oldDigest := fmt.Sprintf("%x", sha256.Sum256([]byte("original-live-dtb")))
	if !strings.Contains(string(entry), "devicetree /EFI/lexr-recovery/"+popTestRootUUID+"/"+oldDigest+"/devicetree.dtb\n") {
		t.Fatalf("recovery entry did not retain original DTB: %s", entry)
	}
	after, err := os.ReadFile(native)
	if err != nil || string(after) != string(before) {
		t.Fatal("native Distinst entry changed")
	}
}

// TestPopRecoveryRejectsIncompleteOrUnownedState prevents publication when
// Distinst's copy differs or an existing entry is outside Lexr ownership.
func TestPopRecoveryRejectsIncompleteOrUnownedState(t *testing.T) {
	for _, testcase := range []struct{ name, path string }{
		{"different live kernel", "recovery/casper-" + recoveryTestSerial + "/vmlinuz.efi"},
		{"different ESP initrd", "boot/efi/EFI/Recovery-" + recoveryTestSerial + "/initrd.gz"},
		{"wrong root identity", "recovery/recovery.conf"},
		{"unowned recovery entry", "boot/efi/loader/entries/lexr-recovery-" + popTestRootUUID + "-" + recoveryTestSerial + ".conf"},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			fixture := makeRecoveryFixture(t)
			if err := os.WriteFile(filepath.Join(fixture.root, testcase.path), []byte("user owned contents\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			output, status := runRecoveryFixture(t, fixture)
			if status != 65 {
				t.Fatalf("status %d, want refusal: %s", status, output)
			}
			if _, err := os.Stat(filepath.Join(fixture.esp, "EFI/lexr-recovery")); !os.IsNotExist(err) {
				t.Fatal("rejected recovery produced EFI payload")
			}
		})
	}
}

// TestPopRecoveryCopiesDeclaredCompanion exercises the actual Go inventory
// encoding and refuses to overwrite a changed recovery companion member.
func TestPopRecoveryCopiesDeclaredCompanion(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflict=%t", conflict), func(t *testing.T) {
			fixture := makeRecoveryFixture(t)
			media := filepath.Join(fixture.root, "usr/share/lexr/pop-media")
			manifestPath := filepath.Join(media, "lexr-manifest.json")
			encoded, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var manifest imagecontract.Manifest
			if err := json.Unmarshal(encoded, &manifest); err != nil {
				t.Fatal(err)
			}
			record := func(path string) imagecontract.ArtifactRecord {
				t.Helper()
				path = "sp11/companion/" + path
				file := filepath.Join(media, filepath.FromSlash(path))
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(path), 0o644); err != nil {
					t.Fatal(err)
				}
				return imagecontract.ArtifactRecord{Path: path, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(path))), Size: int64(len(path))}
			}
			executable := record("bin/linux-arm64/lexr")
			source := record("source/lexr.tar.gz")
			manifest.CompanionBundle = imagecontract.CompanionBundleRecord{
				Included: true, Root: "sp11/companion",
				Executable:    &imagecontract.ExecutableArtifactRecord{Artifact: executable},
				SourceArchive: &source,
				Catalogues:    []imagecontract.ArtifactRecord{record("catalogues/supported-isos.json")},
				Licences:      []imagecontract.ArtifactRecord{record("licences/NOTICE")},
				Userspace:     []imagecontract.OfflineUserspaceRecord{},
			}
			encoded, err = json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(fixture.root, "recovery", filepath.FromSlash(source.Path))
			if conflict {
				if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(destination, []byte("retained user file"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			output, status := runRecoveryFixture(t, fixture)
			if conflict {
				if status != 65 {
					t.Fatalf("modified companion accepted: %d %s", status, output)
				}
				contents, err := os.ReadFile(destination)
				if err != nil || string(contents) != "retained user file" {
					t.Fatal("changed companion overwritten")
				}
				if _, err := os.Stat(filepath.Join(fixture.root, "recovery", executable.Path)); !os.IsNotExist(err) {
					t.Fatal("companion copy began before complete preflight")
				}
				return
			}
			if status != 0 {
				t.Fatalf("companion recovery failed: %d %s", status, output)
			}
			for _, expected := range []imagecontract.ArtifactRecord{executable, source, manifest.CompanionBundle.Catalogues[0], manifest.CompanionBundle.Licences[0]} {
				contents, err := os.ReadFile(filepath.Join(fixture.root, "recovery", expected.Path))
				if err != nil || string(contents) != expected.Path {
					t.Fatalf("recovery companion %s differs: %v", expected.Path, err)
				}
			}
		})
	}
}
