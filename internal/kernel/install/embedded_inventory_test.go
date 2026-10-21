package install

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// requireInstalledDTBDiscovery limits new discovery fixtures to its supported
// host platforms; other hosts retain the existing Surface-only consumer path.
func requireInstalledDTBDiscovery(t *testing.T) {
	t.Helper()
	if !supportsInstalledDTBDiscovery {
		t.Skip("anchored same-ABI discovery requires Linux or macOS")
	}
}

// TestVerifyFallbackAcceptsEmbeddedMultiDeviceInventory recreates a Stubble
// image with many boards, not just the two required Surface variants.
func TestVerifyFallbackAcceptsEmbeddedMultiDeviceInventory(t *testing.T) {
	requireInstalledDTBDiscovery(t)
	root, _ := fixtureEnvironment(t)
	populateEmbeddedMultiDeviceFallback(t, root)
	for _, profile := range []string{"x1e80100-microsoft-denali-oled", "x1p64100-microsoft-denali"} {
		evidence, err := verifyFallbackProfile(fixtureContext(), root, fixtureFallbackABI, profile)
		if err != nil {
			t.Fatal(err)
		}
		if evidence.DeviceTreeBoot.Mode != DeviceTreeBootEmbedded || len(evidence.DeviceTreeBoot.SHA256s) != 39 ||
			evidence.DeviceTreeBoot.NormalGRUBEntryCount != 1 || evidence.DeviceTreeBoot.RecoveryGRUBEntryCount != 1 {
			t.Fatalf("multi-device fallback evidence = %+v", evidence.DeviceTreeBoot)
		}
		if !slices.Contains(evidence.DeviceTreeBoot.SHA256s, digestText("fallback oled dtb")) {
			t.Fatal("selected Surface payload lost from complete embedded evidence")
		}
	}
}

// populateEmbeddedMultiDeviceFallback adds the same complete inventory to both
// direct-verifier and manager fixtures without changing the target bundle.
func populateEmbeddedMultiDeviceFallback(t *testing.T, root string) {
	t.Helper()
	payloads := [][]byte{[]byte("fallback oled dtb"), []byte("fallback lcd dtb")}
	for index := 0; index < 37; index++ {
		payload := fmt.Sprintf("other board DTB %02d", index)
		payloads = append(payloads, []byte(payload))
		writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI,
			fmt.Sprintf("device-tree/qcom/other-board-%02d.dtb", index)), payload)
	}
	// Packaged but non-embedded DTBs and overlays are not supported-device claims.
	writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/not-embedded.dtb"), "not embedded")
	writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/overlay.dtbo"), "fallback oled dtb")
	writeFixtureEmbeddedDTBImageSet(t, filepath.Join(root, "boot/vmlinuz-"+fixtureFallbackABI), payloads)
	grub := strings.Replace(fixtureGRUB(false), " devicetree /dtb-"+fixtureFallbackABI+"\n", "", 1) +
		"menuentry 'Ubuntu " + fixtureFallbackABI + " (recovery mode)' {\n linux /boot/vmlinuz-" + fixtureFallbackABI + " single\n initrd /boot/initrd.img-" + fixtureFallbackABI + "\n}\n"
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
}

// TestManagerPreflightAndDryRunAcceptEmbeddedMultiDeviceFallback proves the
// public installer consumes the same inventory without provisioning an external
// fallback DTB or running a mutation command.
func TestManagerPreflightAndDryRunAcceptEmbeddedMultiDeviceFallback(t *testing.T) {
	requireInstalledDTBDiscovery(t)
	root, request, _ := surfaceInstallationFixture(t, "x1e80100-microsoft-denali-oled")
	populateEmbeddedMultiDeviceFallback(t, root)
	runner := &fakeRunner{root: root}
	manager := fixtureManager(runner)
	plan, err := manager.Preflight(fixtureContext(), request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Fallback.DeviceTreeBoot.Mode != DeviceTreeBootEmbedded || len(plan.Fallback.DeviceTreeBoot.SHA256s) != 39 || plan.FallbackBinding != nil {
		t.Fatalf("multi-device fallback plan = %+v", plan)
	}
	if _, err := manager.Install(fixtureContext(), request); err != nil {
		t.Fatal(err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("dry run executed mutation commands: %+v", runner.runs)
	}
}

// TestEmbeddedDiscoveryRejectsUnattributablePayloads preserves exact same-ABI
// and unique-byte attribution while allowing unrelated packaged boards.
func TestEmbeddedDiscoveryRejectsUnattributablePayloads(t *testing.T) {
	requireInstalledDTBDiscovery(t)
	for _, test := range []struct {
		name   string
		change func(*testing.T, string)
	}{
		{"missing", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/x1e80100-microsoft-denali-oled.dtb")); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong ABI", func(t *testing.T, root string) {
			from := filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/x1e80100-microsoft-denali-oled.dtb")
			to := filepath.Join(root, "usr/lib/firmware", fixtureTargetABI, "device-tree/qcom/x1e80100-microsoft-denali-oled.dtb")
			writeFixtureFile(t, to, "fallback oled dtb")
			if err := os.Remove(from); err != nil {
				t.Fatal(err)
			}
		}},
		{"unmatched bytes", func(t *testing.T, root string) {
			writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/x1e80100-microsoft-denali-oled.dtb"), "different DTB")
		}},
		{"duplicate digest", func(t *testing.T, root string) {
			writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/alias.dtb"), "fallback oled dtb")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := fixtureEnvironment(t)
			populateEmbeddedMultiDeviceFallback(t, root)
			test.change(t, root)
			if _, err := verifyFallback(fixtureContext(), root, fixtureFallbackABI); err == nil {
				t.Fatal("unattributable embedded DTB unexpectedly verified")
			}
		})
	}
}

// TestEmbeddedDiscoveryPreservesSelectedProfile proves other embedded boards
// do not substitute for the required Surface variant.
func TestEmbeddedDiscoveryPreservesSelectedProfile(t *testing.T) {
	requireInstalledDTBDiscovery(t)
	root, _ := fixtureEnvironment(t)
	writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/other-board.dtb"), "other board")
	writeFixtureEmbeddedDTBImage(t, filepath.Join(root, "boot/vmlinuz-"+fixtureFallbackABI), []byte("other board"))
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), strings.Replace(fixtureGRUB(false), " devicetree /dtb-"+fixtureFallbackABI+"\n", "", 1))
	if _, err := verifyFallbackProfile(fixtureContext(), root, fixtureFallbackABI, "x1e80100-microsoft-denali-oled"); err == nil || !strings.Contains(err.Error(), "does not match selected profile") {
		t.Fatalf("wrong-profile embedded error = %v", err)
	}
}

// TestEmbeddedDiscoveryBounds exercises every traversal and byte budget with
// tiny private limits instead of allocating production-sized fixtures.
func TestEmbeddedDiscoveryBounds(t *testing.T) {
	requireInstalledDTBDiscovery(t)
	for _, test := range []struct {
		name  string
		limit func(*installedDTBLimits)
	}{
		{"file bytes", func(l *installedDTBLimits) { l.fileBytes = 1 }},
		{"total bytes", func(l *installedDTBLimits) { l.totalBytes = 1 }},
		{"DTB count", func(l *installedDTBLimits) { l.files = 1 }},
		{"entries", func(l *installedDTBLimits) { l.entries = 1 }},
		{"directories", func(l *installedDTBLimits) { l.directories = 1 }},
		{"depth", func(l *installedDTBLimits) { l.depth = 1 }},
		{"path bytes", func(l *installedDTBLimits) { l.pathBytes = 3 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := fixtureEnvironment(t)
			writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/nested/board.dtb"), "nested")
			limits := defaultInstalledDTBLimits()
			test.limit(&limits)
			if _, err := discoverInstalledDeviceTrees(fixtureContext(), root, fixtureFallbackABI, limits); err == nil {
				t.Fatal("inventory budget unexpectedly exceeded")
			}
		})
	}
}

// TestEmbeddedDiscoveryAccumulatesByteBudget isolates the aggregate bound:
// each DTB fits its individual limit, and only their combined size exceeds it.
func TestEmbeddedDiscoveryAccumulatesByteBudget(t *testing.T) {
	requireInstalledDTBDiscovery(t)
	root := t.TempDir()
	for name, payload := range map[string]string{"one.dtb": "one", "two.dtb": "two"} {
		writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom", name), payload)
	}
	limits := defaultInstalledDTBLimits()
	limits.fileBytes, limits.totalBytes = 3, 6
	if found, err := discoverInstalledDeviceTrees(fixtureContext(), root, fixtureFallbackABI, limits); err != nil || len(found) != 2 {
		t.Fatalf("exact aggregate boundary = %v, %v", found, err)
	}
	limits.totalBytes = 5
	if _, err := discoverInstalledDeviceTrees(fixtureContext(), root, fixtureFallbackABI, limits); err == nil {
		t.Fatal("two individually valid DTBs exceeded the aggregate budget")
	}
}

// TestEmbeddedDiscoveryRejectsUnsafeRoutes checks no-follow reads before data
// access, including a link to another ABI and a non-regular DTB leaf.
func TestEmbeddedDiscoveryRejectsUnsafeRoutes(t *testing.T) {
	requireInstalledDTBDiscovery(t)
	for _, test := range []struct {
		name   string
		change func(*testing.T, string, string)
	}{
		{"file symlink", func(t *testing.T, root, base string) {
			other := filepath.Join(root, "usr/lib/firmware", fixtureTargetABI, "device-tree/qcom/board.dtb")
			writeFixtureFile(t, other, "outside ABI")
			if err := os.Symlink(other, filepath.Join(base, "qcom/link.dtb")); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory symlink", func(t *testing.T, root, base string) {
			if err := os.Symlink(filepath.Join(base, "qcom"), filepath.Join(base, "linked")); err != nil {
				t.Fatal(err)
			}
		}},
		{"vendor-parent symlink", func(t *testing.T, root, base string) {
			vendor := filepath.Join(base, "qcom")
			if err := os.Rename(vendor, vendor+"-original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(vendor+"-original", vendor); err != nil {
				t.Fatal(err)
			}
		}},
		{"base symlink", func(t *testing.T, root, base string) {
			if err := os.Rename(base, base+"-original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(base+"-original", base); err != nil {
				t.Fatal(err)
			}
		}},
		{"empty DTB", func(t *testing.T, root, base string) {
			writeFixtureFile(t, filepath.Join(base, "qcom/empty.dtb"), "")
		}},
		{"unsafe name", func(t *testing.T, root, base string) {
			writeFixtureFile(t, filepath.Join(base, "qcom/bad name.dtb"), "bad")
		}},
		{"vendorless DTB", func(t *testing.T, root, base string) {
			writeFixtureFile(t, filepath.Join(base, "board.dtb"), "bad")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := fixtureEnvironment(t)
			base := filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree")
			test.change(t, root, base)
			if _, err := discoverInstalledDeviceTrees(fixtureContext(), root, fixtureFallbackABI, defaultInstalledDTBLimits()); err == nil {
				t.Fatal("unsafe device-tree route unexpectedly accepted")
			}
		})
	}
	root, _ := fixtureEnvironment(t)
	dir, err := openInstalledDTBDirectory(root, "usr/lib/firmware/"+fixtureFallbackABI+"/device-tree")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if file, err := openInstalledDTBChild(dir, "qcom", false); err == nil {
		file.Close()
		t.Fatal("non-regular DTB read accepted a directory")
	}
}

// TestEmbeddedDiscoveryCancellation honours cancellation before filesystem
// traversal and between successive bounded traversal operations.
func TestEmbeddedDiscoveryCancellation(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	ctx, cancel := context.WithCancel(fixtureContext())
	cancel()
	if _, err := discoverInstalledDeviceTrees(ctx, root, fixtureFallbackABI, defaultInstalledDTBLimits()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled inventory error = %v", err)
	}
	if supportsInstalledDTBDiscovery {
		active, stop := context.WithCancel(fixtureContext())
		defer stop()
		progress := &inventoryCancelContext{Context: active, cancel: stop, cancelAt: 9}
		if _, err := discoverInstalledDeviceTrees(progress, root, fixtureFallbackABI, defaultInstalledDTBLimits()); !errors.Is(err, context.Canceled) || progress.checks != progress.cancelAt {
			t.Fatalf("mid-inspection cancellation error = %v, checks=%d", err, progress.checks)
		}
	}
}

// inventoryCancelContext cancels a real child context after bounded work has
// begun, without goroutine timing or a mutable production hook.
type inventoryCancelContext struct {
	context.Context
	cancel           context.CancelFunc
	checks, cancelAt int
}

// Err triggers the requested cancellation while preserving Context semantics.
func (ctx *inventoryCancelContext) Err() error {
	ctx.checks++
	if ctx.checks == ctx.cancelAt {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

// TestEmbeddedSectionsRejectAllocationAmplification rejects raw bounds beyond
// the image, excessive section counts and payloads above the producer limit.
func TestEmbeddedSectionsRejectAllocationAmplification(t *testing.T) {
	requireInstalledDTBDiscovery(t)
	for _, test := range []struct {
		name   string
		change func([]byte) []byte
	}{
		{"huge raw section", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[20+16:20+20], ^uint32(0)); return b }},
		{"oversized payload", func(b []byte) []byte {
			// Both sizes and the complete raw extent are valid according to the
			// old structural checks; only the new producer-aligned cap rejects it.
			size := uint32(maximumInstalledDTBBytes + 1)
			image := make([]byte, 20+40+int(size))
			copy(image, b)
			binary.LittleEndian.PutUint32(image[20+8:20+12], size)
			binary.LittleEndian.PutUint32(image[20+16:20+20], size)
			return image
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := fixtureEnvironment(t)
			image := filepath.Join(root, "boot/vmlinuz-"+fixtureFallbackABI)
			writeFixtureEmbeddedDTBImage(t, image, []byte("fallback oled dtb"))
			data, err := os.ReadFile(image)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(image, test.change(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := verifyEmbeddedDeviceTreeBinding(fixtureContext(), root, fixtureFallbackABI, nil); err == nil || !strings.Contains(err.Error(), "invalid bounded payload size") {
				t.Fatalf("malicious embedded bounds error = %v", err)
			}
		})
	}
	root, _ := fixtureEnvironment(t)
	payloads := make([][]byte, maximumEmbeddedDTBSections+1)
	for i := range payloads {
		payloads[i] = []byte(fmt.Sprintf("board %d", i))
		writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, fmt.Sprintf("device-tree/qcom/board-%d.dtb", i)), string(payloads[i]))
	}
	writeFixtureEmbeddedDTBImageSet(t, filepath.Join(root, "boot/vmlinuz-"+fixtureFallbackABI), payloads)
	if _, err := verifyEmbeddedDeviceTreeBinding(fixtureContext(), root, fixtureFallbackABI, nil); err == nil || !strings.Contains(err.Error(), "invalid bounded payload size") {
		t.Fatalf("excessive embedded section count error = %v", err)
	}
}
