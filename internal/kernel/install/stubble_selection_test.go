package install

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// installedOrderedFixture is a structural software fixture, not a boot or
// hardware qualification. Both sections use real bounded FDT/HWID encodings.
func installedOrderedFixture(t *testing.T) ([]byte, []kernel.DeviceTree, *kernel.StubbleSelection) {
	t.Helper()
	compatible := "vendor,fixture"
	values := []byte(compatible + "\x00qcom,fixture\x00")
	structure := make([]byte, 8+12+(len(values)+3)&^3+8)
	binary.BigEndian.PutUint32(structure, 1)
	binary.BigEndian.PutUint32(structure[8:], 3)
	binary.BigEndian.PutUint32(structure[12:], uint32(len(values)))
	copy(structure[20:], values)
	binary.BigEndian.PutUint32(structure[len(structure)-8:], 2)
	binary.BigEndian.PutUint32(structure[len(structure)-4:], 9)
	stringsBlock := []byte("compatible\x00")
	dtb := make([]byte, 56+len(structure)+len(stringsBlock))
	for offset, value := range map[int]uint32{0: 0xd00dfeed, 4: uint32(len(dtb)), 8: 56, 12: uint32(56 + len(structure)), 16: 40, 20: 17, 24: 16, 32: uint32(len(stringsBlock)), 36: uint32(len(structure))} {
		binary.BigEndian.PutUint32(dtb[offset:], value)
	}
	copy(dtb[56:], structure)
	copy(dtb[56+len(structure):], stringsBlock)
	table := make([]byte, 56+len("fixture\x00")+len(compatible)+1)
	binary.LittleEndian.PutUint32(table, 0x1000001c)
	copy(table[4:], []byte{0x33, 0x22, 0x11, 0x00, 0x55, 0x44, 0x77, 0x66, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
	binary.LittleEndian.PutUint32(table[20:], 56)
	binary.LittleEndian.PutUint32(table[24:], 64)
	copy(table[56:], "fixture\x00"+compatible+"\x00")
	image := make([]byte, 1536)
	copy(image, "MZ")
	binary.LittleEndian.PutUint32(image[0x3c:], 0x80)
	copy(image[0x80:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(image[0x84:], 0xaa64)
	binary.LittleEndian.PutUint16(image[0x86:], 2)
	binary.LittleEndian.PutUint16(image[0x94:], 240)
	binary.LittleEndian.PutUint16(image[0x96:], 0x22)
	optional := image[0x98:0x188]
	binary.LittleEndian.PutUint16(optional, 0x20b)
	binary.LittleEndian.PutUint32(optional[16:], 0x1000)
	binary.LittleEndian.PutUint64(optional[24:], 0x400000)
	binary.LittleEndian.PutUint32(optional[32:], 4096)
	binary.LittleEndian.PutUint32(optional[36:], 512)
	binary.LittleEndian.PutUint32(optional[56:], 0x3000)
	binary.LittleEndian.PutUint32(optional[60:], 512)
	binary.LittleEndian.PutUint16(optional[68:], 10)
	binary.LittleEndian.PutUint32(optional[108:], 16)
	for index, section := range []struct {
		name string
		data []byte
	}{{".dtbauto", dtb}, {".hwids", table}} {
		header := image[0x188+index*40 : 0x188+(index+1)*40]
		copy(header, section.name)
		binary.LittleEndian.PutUint32(header[8:], uint32(len(section.data)))
		binary.LittleEndian.PutUint32(header[12:], uint32((index+1)*4096))
		binary.LittleEndian.PutUint32(header[16:], 512)
		binary.LittleEndian.PutUint32(header[20:], uint32((index+1)*512))
		copy(image[(index+1)*512:], section.data)
	}
	trees := []kernel.DeviceTree{{Device: "fixture-board", Basename: "fixture.dtb", Path: "usr/lib/firmware/" + fixtureTargetABI + "/device-tree/qcom/fixture.dtb", SHA256: digestText(string(dtb)), CompatibleStrings: []string{compatible, "qcom,fixture"}, EmbeddedMatches: 1, Required: true}}
	observed, err := kernel.ObserveStubbleSelection(image, trees)
	if err != nil {
		t.Fatal(err)
	}
	return image, trees, observed
}

// TestInstalledOrderedSelectionChecksActualBytes covers the final install
// gate which precedes success/default promotion and uses rollback on failure.
func TestInstalledOrderedSelectionChecksActualBytes(t *testing.T) {
	image, trees, selection := installedOrderedFixture(t)
	root := t.TempDir()
	file := filepath.Join(root, "boot", "vmlinuz-"+fixtureTargetABI)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, image, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyInstalledStubbleSelection(fixtureContext(), root, fixtureTargetABI, selection, trees); err != nil {
		t.Fatal(err)
	}
	changed := *selection
	changed.HWIDRecords = append([]kernel.StubbleHWIDRecord(nil), selection.HWIDRecords...)
	changed.HWIDRecords[0].Compatible = "vendor,other"
	if err := verifyInstalledStubbleSelection(fixtureContext(), root, fixtureTargetABI, &changed, trees); err == nil {
		t.Fatal("manifest-only table assertion was accepted")
	}
	image[len(image)-1] ^= 1
	if err := os.WriteFile(file, image, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyInstalledStubbleSelection(fixtureContext(), root, fixtureTargetABI, selection, trees); err == nil {
		t.Fatal("changed installed image was accepted")
	}
}

// TestPlannedOrderedSelectionOwnsItsInputs prevents caller mutation of a
// previously reviewed manifest from changing the final comparison authority.
func TestPlannedOrderedSelectionOwnsItsInputs(t *testing.T) {
	_, trees, selection := installedOrderedFixture(t)
	bundle := kernel.Bundle{DeviceTrees: trees, DTBSelectionProvenance: &kernel.DTBSelectionProvenance{StubbleSelection: selection}}
	planned, plannedTrees := plannedStubbleSelection(bundle)
	if !reflect.DeepEqual(planned, selection) || !reflect.DeepEqual(plannedTrees, trees) {
		t.Fatal("planned evidence differs")
	}
	selection.PayloadOrder[0] = "changed"
	selection.HWIDRecords[0].Compatible = "changed"
	trees[0].CompatibleStrings[0] = "changed"
	if planned.PayloadOrder[0] == "changed" || planned.HWIDRecords[0].Compatible == "changed" || plannedTrees[0].CompatibleStrings[0] == "changed" {
		t.Fatal("planned evidence aliases caller memory")
	}
	if err := verifyInstalledStubbleSelection(fixtureContext(), t.TempDir(), fixtureTargetABI, nil, nil); err != nil {
		t.Fatalf("legacy no-policy path changed: %v", err)
	}
}

// TestOrderedImageMismatchRollsBackTransaction exercises the manager's actual
// failure branch, retaining the fallback and original GRUB configuration.
func TestOrderedImageMismatchRollsBackTransaction(t *testing.T) {
	root, base := fixtureEnvironment(t)
	image, trees, selection := installedOrderedFixture(t)
	trees[0].Selectors = []kernel.DeviceTreeSelector{
		{Kind: kernel.DeviceTreeSelectorCompatible, Value: trees[0].CompatibleStrings[0]},
		{Kind: kernel.DeviceTreeSelectorHWID, Value: selection.HWIDRecords[0].HWID},
	}
	provenance := fixtureEmbeddedBundle(t, base).DTBSelectionProvenance
	provenance.Tool = "stubble"
	provenance.StubbleSelection = selection
	provenance.Selections = []kernel.DeviceTreeSelectionEvidence{{Device: trees[0].Device,
		Records: []kernel.DTBSelectionRecord{{Source: "hwids", Compatible: trees[0].CompatibleStrings[0], HWIDs: []string{selection.HWIDRecords[0].HWID}}}}}
	var packages []kernel.Package
	for _, item := range base.Packages {
		if item.Role != kernel.RoleBootSupport {
			packages = append(packages, item)
		}
	}
	bundle, err := kernel.NewBundle(kernel.BundleOptions{Release: base.Release,
		RequestedBootImageMode: kernel.RequestedBootImageModeStubble, EffectiveDTBDelivery: kernel.DTBDeliveryEmbedded,
		EmbeddedDTBCount: 1, Packages: packages, DeviceTrees: trees, DTBSelectionProvenance: provenance})
	if err != nil {
		t.Fatal(err)
	}
	grubPath := filepath.Join(root, "boot/grub/grub.cfg")
	originalGRUB, err := os.ReadFile(grubPath)
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := verifyFallback(fixtureContext(), root, fixtureFallbackABI)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{root: root}
	runner.runHook = func(_ context.Context, command platform.Command) error {
		switch {
		case slicesContain(command.Args, "--install"):
			if err := installFixtureTarget(root); err != nil {
				return err
			}
			payloadSize := binary.LittleEndian.Uint32(image[0x188+8:])
			writeFixtureFile(t, filepath.Join(root, trees[0].Path), string(image[512:512+payloadSize]))
			changed := append([]byte(nil), image...)
			changed[len(changed)-1] ^= 1
			writeFixtureFile(t, filepath.Join(root, "boot/vmlinuz-"+fixtureTargetABI), string(changed))
			grub := strings.ReplaceAll(fixtureGRUB(true), " devicetree /boot/dtb-"+fixtureTargetABI+"\n", "")
			writeFixtureFile(t, grubPath, grub)
		case command.Name == chrootCommand && slicesContain(command.Args, updateInitramfsCommand):
			writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI), "target initramfs")
		case slicesContain(command.Args, "--purge"):
			return removeFixtureTarget(root)
		}
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(fixtureContext(), fixtureRequest(root, bundle, false))
	if err == nil || !strings.Contains(err.Error(), "installed Stubble inventory order differs") {
		t.Fatalf("ordered transaction failure = %v", err)
	}
	if receipt.Rollback == nil || !receipt.Rollback.Attempted || !receipt.Rollback.GRUBRestored || receipt.Rollback.Error != "" || receipt.Installed != nil || receipt.RebootRequired {
		t.Fatalf("ordered mismatch reached success or failed rollback: %+v", receipt)
	}
	actualGRUB, err := os.ReadFile(grubPath)
	if err != nil || string(actualGRUB) != string(originalGRUB) {
		t.Fatalf("original GRUB was not restored: %v", err)
	}
	currentFallback, err := verifyFallback(fixtureContext(), root, fixtureFallbackABI)
	if err != nil || fallbackPreservedAfterInstall(fallback, currentFallback) != nil {
		t.Fatalf("fallback was not preserved: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "boot/vmlinuz-"+fixtureTargetABI)); !os.IsNotExist(err) {
		t.Fatalf("failed target image remains: %v", err)
	}
}
