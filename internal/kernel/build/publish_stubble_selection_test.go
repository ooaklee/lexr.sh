package build

import (
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// The producer measures physical payload order only for shared-route embedded
// images. The data-only PE fixture is not bootable firmware.
func TestReadProvenanceObservesSharedStubbleImage(t *testing.T) {
	t.Parallel()
	image, trees := producerStubbleFixture()
	transaction, plan := writeSharedStubbleProvenance(t, trees)
	imagePath := filepath.Join(transaction, "provenance", "stubble-inspected-image.efi")
	if err := os.WriteFile(imagePath, image, 0600); err != nil {
		t.Fatal(err)
	}
	provenance, err := readProvenance(transaction, plan)
	if err != nil {
		t.Fatal(err)
	}
	got := provenance.DTBSelectionProvenance.StubbleSelection
	if got == nil || got.ImageSHA256 != producerImageDigest(image) ||
		!reflect.DeepEqual(got.PayloadOrder, []string{"fixture-zeta", "fixture-alpha"}) {
		t.Fatalf("producer did not retain actual image order: %#v", got)
	}
	if !reflect.DeepEqual(provenance.DeviceTrees, kernel.CloneDeviceTrees(trees)) {
		t.Fatal("producer changed canonical device-tree records")
	}
	if err := kernel.VerifyStubbleSelection(image, provenance.DeviceTrees, got); err != nil {
		t.Fatal(err)
	}
	got.PayloadOrder[0], got.PayloadOrder[1] = got.PayloadOrder[1], got.PayloadOrder[0]
	if err := kernel.VerifyStubbleSelection(image, provenance.DeviceTrees, got); err == nil {
		t.Fatal("accepted a claimed order different from the stored image")
	}
}

// TestReadProvenanceSharedStubbleRejectsMissingAndWrongImage keeps measured
// order evidence fail-closed when its public image is missing or inconsistent.
func TestReadProvenanceSharedStubbleRejectsMissingAndWrongImage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func([]byte, []kernel.DeviceTree) ([]byte, []kernel.DeviceTree)
		missing bool
	}{
		{name: "missing image", missing: true},
		{name: "malformed image", mutate: func(_ []byte, v []kernel.DeviceTree) ([]byte, []kernel.DeviceTree) { return []byte("not a PE"), v }},
		{name: "wrong payload digest", mutate: func(b []byte, v []kernel.DeviceTree) ([]byte, []kernel.DeviceTree) {
			v[0].SHA256 = strings.Repeat("e", 64)
			return b, v
		}},
		{name: "wrong compatible order", mutate: func(b []byte, v []kernel.DeviceTree) ([]byte, []kernel.DeviceTree) {
			v[0].CompatibleStrings = []string{"qcom,fixture", "vendor,alpha"}
			return b, v
		}},
		{name: "missing HWID table", mutate: func(b []byte, v []kernel.DeviceTree) ([]byte, []kernel.DeviceTree) {
			copy(b[producerSectionTable+2*40:], []byte(".unused\x00"))
			return b, v
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			image, trees := producerStubbleFixture()
			if test.mutate != nil {
				image, trees = test.mutate(image, trees)
			}
			transaction, plan := writeSharedStubbleProvenance(t, trees)
			if !test.missing {
				if err := os.WriteFile(filepath.Join(transaction, "provenance", "stubble-inspected-image.efi"), image, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := readProvenance(transaction, plan); err == nil {
				t.Fatal("accepted missing or mismatched inspection image")
			}
		})
	}
}

// TestReadProvenanceUniqueRoutesDoNotRequireInspectionImage preserves the
// existing unique-route producer contract without requiring a new image read.
func TestReadProvenanceUniqueRoutesDoNotRequireInspectionImage(t *testing.T) {
	t.Parallel()
	_, trees := producerStubbleFixture()
	trees[1].Selectors[1].Value = "11111111-1111-5111-8111-111111111111"
	transaction, plan := writeSharedStubbleProvenance(t, trees)
	provenance, err := readProvenance(transaction, plan)
	if err != nil {
		t.Fatal(err)
	}
	if provenance.DTBSelectionProvenance.StubbleSelection != nil {
		t.Fatal("unique-route schema-2 producer acquired ordered-inventory claims")
	}
}

// TestReadStubbleInspectionImageFileBoundary rejects nonregular, redirected
// and oversized files before allocating or observing their contents.
func TestReadStubbleInspectionImageFileBoundary(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{"empty", func(t *testing.T, p string) {
			if err := os.WriteFile(p, nil, 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory", func(t *testing.T, p string) {
			if err := os.Mkdir(p, 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"oversized", func(t *testing.T, p string) {
			f, err := os.Create(p)
			if err != nil {
				t.Fatal(err)
			}
			err = f.Truncate(maximumStubbleInspectionImageBytes + 1)
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("create sparse bound fixture: %v/%v", err, closeErr)
			}
		}},
		{"symlink", func(t *testing.T, p string) {
			if runtime.GOOS == "windows" {
				t.Skip("symlink privilege is not universally available")
			}
			target := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, p); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transaction := t.TempDir()
			dir := filepath.Join(transaction, "provenance")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			test.setup(t, filepath.Join(dir, "stubble-inspected-image.efi"))
			if _, err := readStubbleInspectionImage(transaction); err == nil {
				t.Fatal("accepted unsafe inspection image")
			}
		})
	}
	t.Run("symlink provenance directory", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink privilege is not universally available")
		}
		transaction := t.TempDir()
		if err := os.Symlink(t.TempDir(), filepath.Join(transaction, "provenance")); err != nil {
			t.Fatal(err)
		}
		if _, err := readStubbleInspectionImage(transaction); err == nil {
			t.Fatal("followed provenance directory symlink")
		}
	})
}

// TestStubbleInspectionRecipeExportsExactPublicImage checks the exact recipe
// transfer and verifies that a failed embedded-image copy stops production.
func TestStubbleInspectionRecipeExportsExactPublicImage(t *testing.T) {
	t.Parallel()
	const block = "if [ \"$effective_delivery\" = embedded ]; then\n  cp -- \"$inspection_image\" \"$provenance_dir/stubble-inspected-image.efi\"\nfi"
	if strings.Count(containerRecipe, block) != 1 || !strings.Contains(containerRecipe, "PY_SELECTION\n"+block) {
		t.Fatal("durable image transfer is not the exact post-inspection recipe block")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is unavailable for the recipe transfer fixture")
	}
	for _, test := range []struct {
		name, delivery string
		sourcePresent  bool
		wantError      bool
	}{{"embedded", "embedded", true, false}, {"external", "external-required", false, false}, {"copy failure", "embedded", false, true}} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			source := filepath.Join(directory, "source-image")
			if test.sourcePresent {
				if err := os.WriteFile(source, []byte("exact public image bytes"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command(bash, "-s", "--", test.delivery, source, directory)
			command.Stdin = strings.NewReader("set -euo pipefail\neffective_delivery=$1\ninspection_image=$2\nprovenance_dir=$3\n" + block + "\nprintf 'finished'\n")
			output, err := command.CombinedOutput()
			if test.wantError {
				if err == nil || strings.Contains(string(output), "finished") {
					t.Fatal("image transfer failure did not stop the producer")
				}
				return
			}
			if err != nil || string(output) != "finished" {
				t.Fatalf("recipe transfer failed: %v", err)
			}
			copied, err := os.ReadFile(filepath.Join(directory, "stubble-inspected-image.efi"))
			if test.delivery == "embedded" {
				if err != nil || string(copied) != "exact public image bytes" {
					t.Fatal("durable inspection image differs from the extracted public image")
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("external delivery unexpectedly retained an inspection image")
			}
		})
	}
}

// writeSharedStubbleProvenance supplies the existing producer identity fields;
// only the image observer adds the new nested schema-3 selection contract.
func writeSharedStubbleProvenance(t *testing.T, trees []kernel.DeviceTree) (string, Plan) {
	t.Helper()
	transaction := t.TempDir()
	directory := filepath.Join(transaction, "provenance")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	plan := Plan{SourceKind: SourceKindHTTPSGit, GitURL: "https://example.invalid/kernel.git", GitRef: "fixture", BootImageMode: BootImageModeStubble, RecipeSHA256: strings.Repeat("9", 64)}
	fields := map[string]string{
		"source-kind": string(plan.SourceKind), "git-url": plan.GitURL, "git-ref": plan.GitRef, "ref-kind": "branch",
		"boot-image-mode": string(plan.BootImageMode), "effective-dtb-delivery": string(kernel.DTBDeliveryEmbedded),
		"embedded-dtb-count": "2", "revision": strings.Repeat("a", 40), "tree": strings.Repeat("b", 40),
		"commit-time": "2026-08-30T10:00:00Z", "recipe-sha256": plan.RecipeSHA256, "toolchain-sha256": strings.Repeat("c", 64),
		"stubble-tool": "stubble", "stubble-version": "9-1", "stubble-stub-sha256": strings.Repeat("1", 64),
		"stubble-helper-sha256": strings.Repeat("2", 64), "stubble-sbat-sha256": strings.Repeat("3", 64),
		"stubble-hwids-sha256": strings.Repeat("d", 64), "ukify-tool": "ukify", "ukify-package": "systemd-ukify",
		"ukify-version": "259.9-0ubuntu0.1", "ukify-sha256": strings.Repeat("4", 64),
	}
	for name, value := range fields {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	evidence := make([]kernel.DeviceTreeSelectionEvidence, 0, len(trees))
	for _, tree := range trees {
		var hwids []string
		for _, selector := range tree.Selectors {
			if selector.Kind == kernel.DeviceTreeSelectorHWID {
				hwids = append(hwids, selector.Value)
			}
		}
		evidence = append(evidence, kernel.DeviceTreeSelectionEvidence{Device: tree.Device, Records: []kernel.DTBSelectionRecord{{Source: "hwids", Compatible: tree.CompatibleStrings[0], HWIDs: hwids}}})
	}
	for name, value := range map[string]any{"device-tree-inventory.json": trees, "device-tree-selection-records.json": evidence} {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return transaction, plan
}

// producerImageDigest hashes only synthetic fixture bytes for attribution.
func producerImageDigest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

// producerStubbleFixture creates two optional model payloads sharing a HWID,
// with stored PE order deliberately different from device-name order.
func producerStubbleFixture() ([]byte, []kernel.DeviceTree) {
	alphaCompatible, zetaCompatible := []string{"vendor,alpha", "qcom,fixture"}, []string{"vendor,zeta", "qcom,fixture"}
	alpha, zeta := producerFDT(alphaCompatible), producerFDT(zetaCompatible)
	hwid := "00112233-4455-6677-8899-aabbccddeeff"
	trees := []kernel.DeviceTree{}
	for _, row := range []struct {
		device     string
		data       []byte
		compatible []string
	}{{"fixture-alpha", alpha, alphaCompatible}, {"fixture-zeta", zeta, zetaCompatible}} {
		trees = append(trees, kernel.DeviceTree{Device: row.device, Basename: row.device + ".dtb", Path: "usr/lib/firmware/" + testKernelABI + "/device-tree/qcom/" + row.device + ".dtb", SHA256: producerImageDigest(row.data), CompatibleStrings: row.compatible, EmbeddedMatches: 1, Selectors: []kernel.DeviceTreeSelector{{Kind: kernel.DeviceTreeSelectorCompatible, Value: row.compatible[0]}, {Kind: kernel.DeviceTreeSelectorHWID, Value: hwid}}})
	}
	return producerPE([]producerSection{{".dtbauto", zeta}, {".dtbauto", alpha}, {".hwids", producerHWIDs([]string{"vendor,zeta", "vendor,alpha"})}}), trees
}

// producerSection names one synthetic public PE data section.
type producerSection struct {
	name string
	data []byte
}

// producerSectionTable follows the fixture's 240-byte PE32+ optional header.
const producerSectionTable = 0x98 + 240

// producerPE emits a data-only AArch64 EFI image layout, not bootable firmware.
func producerPE(sections []producerSection) []byte {
	offset := (producerSectionTable + len(sections)*40 + 511) &^ 511
	image := make([]byte, offset)
	copy(image, "MZ")
	binary.LittleEndian.PutUint32(image[0x3c:], 0x80)
	copy(image[0x80:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(image[0x84:], pe.IMAGE_FILE_MACHINE_ARM64)
	binary.LittleEndian.PutUint16(image[0x86:], uint16(len(sections)))
	binary.LittleEndian.PutUint16(image[0x94:], 240)
	binary.LittleEndian.PutUint16(image[0x98:], 0x20b)
	binary.LittleEndian.PutUint32(image[0x98+16:], 0x1000)
	binary.LittleEndian.PutUint32(image[0x98+32:], 0x1000)
	binary.LittleEndian.PutUint32(image[0x98+36:], 512)
	binary.LittleEndian.PutUint32(image[0x98+56:], uint32((len(sections)+1)*0x1000))
	binary.LittleEndian.PutUint32(image[0x98+60:], uint32(offset))
	binary.LittleEndian.PutUint16(image[0x98+68:], pe.IMAGE_SUBSYSTEM_EFI_APPLICATION)
	binary.LittleEndian.PutUint32(image[0x98+108:], 16)
	for i, section := range sections {
		header := image[producerSectionTable+i*40 : producerSectionTable+(i+1)*40]
		copy(header, section.name)
		binary.LittleEndian.PutUint32(header[8:], uint32(len(section.data)))
		binary.LittleEndian.PutUint32(header[12:], uint32((i+1)*0x1000))
		raw := (len(section.data) + 511) &^ 511
		binary.LittleEndian.PutUint32(header[16:], uint32(raw))
		binary.LittleEndian.PutUint32(header[20:], uint32(offset))
		image = append(image, section.data...)
		image = append(image, make([]byte, raw-len(section.data))...)
		offset += raw
	}
	return image
}

// producerHWIDs writes ordered type-1 table rows with one shared fixture UUID.
func producerHWIDs(compatibles []string) []byte {
	data := make([]byte, (len(compatibles)+1)*28)
	offsets := map[string]uint32{}
	text := func(value string) uint32 {
		if at, ok := offsets[value]; ok {
			return at
		}
		at := uint32(len(data))
		offsets[value] = at
		data = append(data, []byte(value)...)
		data = append(data, 0)
		return at
	}
	for i, compatible := range compatibles {
		name, value := text("Fixture device"), text(compatible)
		at := i * 28
		binary.LittleEndian.PutUint32(data[at:], 0x1000001c)
		copy(data[at+4:at+20], []byte{0x33, 0x22, 0x11, 0, 0x55, 0x44, 0x77, 0x66, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
		binary.LittleEndian.PutUint32(data[at+20:], name)
		binary.LittleEndian.PutUint32(data[at+24:], value)
	}
	return data
}

// producerFDT emits a small valid tree retaining the full compatible order.
func producerFDT(compatibles []string) []byte {
	value := []byte(strings.Join(compatibles, "\x00") + "\x00")
	var structure bytes.Buffer
	for _, number := range []uint32{1, 0, 3, uint32(len(value)), 0} {
		_ = binary.Write(&structure, binary.BigEndian, number)
	}
	structure.Write(value)
	for structure.Len()%4 != 0 {
		structure.WriteByte(0)
	}
	_ = binary.Write(&structure, binary.BigEndian, uint32(2))
	_ = binary.Write(&structure, binary.BigEndian, uint32(9))
	names := []byte("compatible\x00")
	data := make([]byte, 56)
	for at, number := range map[int]uint32{0: 0xd00dfeed, 4: uint32(56 + structure.Len() + len(names)), 8: 56, 12: uint32(56 + structure.Len()), 16: 40, 20: 17, 24: 16, 32: uint32(len(names)), 36: uint32(structure.Len())} {
		binary.BigEndian.PutUint32(data[at:], number)
	}
	data = append(data, structure.Bytes()...)
	return append(data, names...)
}
