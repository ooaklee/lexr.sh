package build

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// recipeScannerABI is the synthetic exact ABI used by DTB archive scanner tests.
const recipeScannerABI = "7.2.0-jg-0sp11v23-qcom-x1e"

// TestRecipeDTBScannerRetainsOnlyExternalPlatformsFromGenericInventory proves
// a normal Ubuntu ARM64 inventory can exceed the historical 1,024-member cap
// without making every unrelated DTB part of Lexr's retained working set.
func TestRecipeDTBScannerRetainsOnlyExternalPlatformsFromGenericInventory(t *testing.T) {
	root := t.TempDir()
	imageArchive := filepath.Join(root, "image.tar")
	modulesArchive := filepath.Join(root, "modules.tar")
	writeRecipeTar(t, imageArchive, nil)
	members := make([]recipeTarMember, 0, 1802)
	for index := 0; index < 1800; index++ {
		name := fmt.Sprintf("./usr/lib/firmware/%s/device-tree/vendor/board-%04d.dtb", recipeScannerABI, index)
		if index == 0 {
			name = "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/vendor/panel,reference.dtb"
		}
		members = append(members, recipeTarMember{
			name: name,
			data: []byte(fmt.Sprintf("generic-%04d", index)),
		})
	}
	members = append(members,
		recipeTarMember{
			name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb",
			data: []byte("oled-dtb"),
		},
		recipeTarMember{
			name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/x1p64100-microsoft-denali.dtb",
			data: []byte("lcd-dtb"),
		},
	)
	writeRecipeTar(t, modulesArchive, members)

	output := filepath.Join(root, "selected.tsv")
	selectedRoot := filepath.Join(root, "selected")
	if err := os.Mkdir(selectedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	sections := filepath.Join(root, "sections.tsv")
	writeRecipeFixture(t, sections, "")
	commandOutput, err := executeRecipeDTBScanner(t, recipeScannerABI, "external-required", sections, output, selectedRoot, imageArchive, modulesArchive)
	if err != nil {
		t.Fatalf("scan generic external inventory: %v\n%s", err, commandOutput)
	}
	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(encoded)), "\n")
	if len(lines) != 2 || !strings.Contains(string(encoded), "x1e80100-microsoft-denali-oled.dtb") ||
		!strings.Contains(string(encoded), "x1p64100-microsoft-denali.dtb") || strings.Contains(string(encoded), "board-0000.dtb") {
		t.Fatalf("selected external inventory = %q", encoded)
	}
	entries, err := os.ReadDir(selectedRoot)
	if err != nil || len(entries) != 2 {
		t.Fatalf("materialised selected DTBs = %d, %v", len(entries), err)
	}
}

// TestRecipeDTBScannerFindsEmbeddedPayloadInGenericInventory proves Stubble
// attribution remains complete while unrelated generic package DTBs stream by.
func TestRecipeDTBScannerFindsEmbeddedPayloadInGenericInventory(t *testing.T) {
	root := t.TempDir()
	imageArchive := filepath.Join(root, "image.tar")
	modulesArchive := filepath.Join(root, "modules.tar")
	writeRecipeTar(t, imageArchive, nil)
	members := make([]recipeTarMember, 0, 1801)
	for index := 0; index < 1800; index++ {
		members = append(members, recipeTarMember{
			name: fmt.Sprintf("./usr/lib/firmware/%s/device-tree/vendor/board-%04d.dtb", recipeScannerABI, index),
			data: []byte(fmt.Sprintf("generic-%04d", index)),
		})
	}
	embedded := []byte("embedded-oled-dtb")
	members = append(members, recipeTarMember{
		name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb",
		data: embedded,
	})
	writeRecipeTar(t, modulesArchive, members)

	sectionFile := filepath.Join(root, "section-1.dtb")
	if err := os.WriteFile(sectionFile, embedded, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(embedded)
	sections := filepath.Join(root, "sections.tsv")
	writeRecipeFixture(t, sections, fmt.Sprintf("1\t%d\t%x\t%s\n", len(embedded), digest, sectionFile))
	output := filepath.Join(root, "selected.tsv")
	selectedRoot := filepath.Join(root, "selected")
	if err := os.Mkdir(selectedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	commandOutput, err := executeRecipeDTBScanner(t, recipeScannerABI, "embedded", sections, output, selectedRoot, imageArchive, modulesArchive)
	if err != nil {
		t.Fatalf("scan generic embedded inventory: %v\n%s", err, commandOutput)
	}
	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.TrimSpace(string(encoded)), "\n") != 0 ||
		!strings.Contains(string(encoded), "x1e80100-microsoft-denali-oled.dtb") ||
		!strings.Contains(string(encoded), "\t1\t") {
		t.Fatalf("selected embedded inventory = %q", encoded)
	}
}

// TestRecipeDTBScannerRejectsMemberCountOverflow proves generic support remains
// bounded independently from the much smaller selected delivery inventory.
func TestRecipeDTBScannerRejectsMemberCountOverflow(t *testing.T) {
	root := t.TempDir()
	imageArchive := filepath.Join(root, "image.tar")
	modulesArchive := filepath.Join(root, "modules.tar")
	writeRecipeTar(t, imageArchive, nil)
	members := make([]recipeTarMember, 0, 4097)
	for index := 0; index < 4097; index++ {
		members = append(members, recipeTarMember{
			name: fmt.Sprintf("./usr/lib/firmware/%s/device-tree/vendor/board-%04d.dtb", recipeScannerABI, index),
			data: []byte("bounded"),
		})
	}
	writeRecipeTar(t, modulesArchive, members)
	sections := filepath.Join(root, "sections.tsv")
	writeRecipeFixture(t, sections, "")
	selectedRoot := filepath.Join(root, "selected")
	if err := os.Mkdir(selectedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	output, err := executeRecipeDTBScanner(t, recipeScannerABI, "external-required", sections,
		filepath.Join(root, "selected.tsv"), selectedRoot, imageArchive, modulesArchive)
	if err == nil || !strings.Contains(string(output), "bounded DTB member count") {
		t.Fatalf("member-count error = %v, output %q", err, output)
	}
}

// TestRecipeDTBScannerRejectsAmbiguousEmbeddedPayload ensures byte-identical
// package paths cannot both claim authority for one embedded Stubble section.
func TestRecipeDTBScannerRejectsAmbiguousEmbeddedPayload(t *testing.T) {
	root := t.TempDir()
	imageArchive := filepath.Join(root, "image.tar")
	modulesArchive := filepath.Join(root, "modules.tar")
	writeRecipeTar(t, imageArchive, nil)
	embedded := []byte("same-embedded-dtb")
	writeRecipeTar(t, modulesArchive, []recipeTarMember{
		{name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/first.dtb", data: embedded},
		{name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/second.dtb", data: embedded},
	})
	sectionFile := filepath.Join(root, "section-1.dtb")
	if err := os.WriteFile(sectionFile, embedded, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(embedded)
	sections := filepath.Join(root, "sections.tsv")
	writeRecipeFixture(t, sections, fmt.Sprintf("1\t%d\t%x\t%s\n", len(embedded), digest, sectionFile))
	selectedRoot := filepath.Join(root, "selected")
	if err := os.Mkdir(selectedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	output, err := executeRecipeDTBScanner(t, recipeScannerABI, "embedded", sections,
		filepath.Join(root, "selected.tsv"), selectedRoot, imageArchive, modulesArchive)
	if err == nil || !strings.Contains(string(output), "matches more than one packaged path") {
		t.Fatalf("ambiguous embedded error = %v, output %q", err, output)
	}
}

// TestRecipeDTBScannerRejectsUnsafeOrUnboundedMembers exercises archive forms
// which must fail before an archive-supplied path can be materialised.
func TestRecipeDTBScannerRejectsUnsafeOrUnboundedMembers(t *testing.T) {
	tests := []struct {
		name    string
		members []recipeTarMember
		want    string
	}{
		{
			name: "absolute path",
			members: []recipeTarMember{{
				name: "/usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/absolute.dtb",
				data: []byte("unsafe"),
			}},
			want: "path is absolute",
		},
		{
			name: "traversal",
			members: []recipeTarMember{{
				name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/../../escape.dtb",
				data: []byte("unsafe"),
			}},
			want: "not canonical",
		},
		{
			name: "empty component",
			members: []recipeTarMember{{
				name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom//empty.dtb",
				data: []byte("unsafe"),
			}},
			want: "not canonical",
		},
		{
			name: "control byte",
			members: []recipeTarMember{{
				name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/control\nname.dtb",
				data: []byte("unsafe"),
			}},
			want: "contains control bytes",
		},
		{
			name: "symlink",
			members: []recipeTarMember{{
				name:     "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/link.dtb",
				typeflag: tar.TypeSymlink,
				linkname: "/etc/passwd",
			}},
			want: "not a regular file",
		},
		{
			name: "hardlink",
			members: []recipeTarMember{{
				name:     "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/hardlink.dtb",
				typeflag: tar.TypeLink,
				linkname: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/target.dtb",
			}},
			want: "not a regular file",
		},
		{
			name: "duplicate path",
			members: []recipeTarMember{
				{
					name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/duplicate.dtb",
					data: []byte("first"),
				},
				{
					name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/duplicate.dtb",
					data: []byte("second"),
				},
			},
			want: "contains a duplicate path",
		},
		{
			name: "wrong ABI",
			members: []recipeTarMember{{
				name: "./usr/lib/firmware/7.2.0-wrong-qcom-x1e/device-tree/qcom/wrong.dtb",
				data: []byte("unsafe"),
			}},
			want: "not scoped to the generated ABI",
		},
		{
			name: "oversized",
			members: []recipeTarMember{{
				name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/large.dtb",
				data: make([]byte, 4*1024*1024+1),
			}},
			want: "per-file size limit",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			imageArchive := filepath.Join(root, "image.tar")
			modulesArchive := filepath.Join(root, "modules.tar")
			writeRecipeTar(t, imageArchive, nil)
			writeRecipeTar(t, modulesArchive, test.members)
			sections := filepath.Join(root, "sections.tsv")
			writeRecipeFixture(t, sections, "")
			selectedRoot := filepath.Join(root, "selected")
			if err := os.Mkdir(selectedRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			output, err := executeRecipeDTBScanner(t, recipeScannerABI, "external-required", sections,
				filepath.Join(root, "selected.tsv"), selectedRoot, imageArchive, modulesArchive)
			if err == nil || !strings.Contains(string(output), test.want) {
				t.Fatalf("unsafe archive error = %v, output %q", err, output)
			}
		})
	}
}

// TestRecipeDTBScannerRejectsIncompleteExternalDelivery proves the scanner
// requires both declared Surface platform DTBs, not merely one valid match.
func TestRecipeDTBScannerRejectsIncompleteExternalDelivery(t *testing.T) {
	root := t.TempDir()
	imageArchive := filepath.Join(root, "image.tar")
	modulesArchive := filepath.Join(root, "modules.tar")
	writeRecipeTar(t, imageArchive, nil)
	writeRecipeTar(t, modulesArchive, []recipeTarMember{{
		name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb",
		data: []byte("oled-only"),
	}})
	sections := filepath.Join(root, "sections.tsv")
	writeRecipeFixture(t, sections, "")
	selectedRoot := filepath.Join(root, "selected")
	if err := os.Mkdir(selectedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	output, err := executeRecipeDTBScanner(t, recipeScannerABI, "external-required", sections,
		filepath.Join(root, "selected.tsv"), selectedRoot, imageArchive, modulesArchive)
	if err == nil || !strings.Contains(string(output), "does not contain exactly one declared DTB per platform") {
		t.Fatalf("incomplete external delivery error = %v, output %q", err, output)
	}
}

// TestRecipeDTBScannerRejectsAggregateByteOverflow exercises the aggregate
// guard with its compiled constant reduced in-memory to keep the fixture tiny.
func TestRecipeDTBScannerRejectsAggregateByteOverflow(t *testing.T) {
	root := t.TempDir()
	imageArchive := filepath.Join(root, "image.tar")
	modulesArchive := filepath.Join(root, "modules.tar")
	writeRecipeTar(t, imageArchive, nil)
	writeRecipeTar(t, modulesArchive, []recipeTarMember{
		{
			name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/vendor/first.dtb",
			data: []byte("first"),
		},
		{
			name: "./usr/lib/firmware/" + recipeScannerABI + "/device-tree/vendor/second.dtb",
			data: []byte("other"),
		},
	})
	sections := filepath.Join(root, "sections.tsv")
	writeRecipeFixture(t, sections, "")
	selectedRoot := filepath.Join(root, "selected")
	if err := os.Mkdir(selectedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	script := recipeDTBScannerScript(t)
	const compiledBound = "MAX_DTB_BYTES = 512 * 1024 * 1024"
	if strings.Count(script, compiledBound) != 1 {
		t.Fatalf("compiled aggregate bound occurrences = %d", strings.Count(script, compiledBound))
	}
	script = strings.Replace(script, compiledBound, "MAX_DTB_BYTES = 8", 1)
	output, err := executeRecipeDTBScannerScript(t, script, recipeScannerABI, "external-required", sections,
		filepath.Join(root, "selected.tsv"), selectedRoot, imageArchive, modulesArchive)
	if err == nil || !strings.Contains(string(output), "bounded aggregate DTB size") {
		t.Fatalf("aggregate-byte error = %v, output %q", err, output)
	}
}

// TestRecipeSelectionAttributionUsesStubbleJSONSemantics proves the container
// recipe binds a DTB compatible to the canonical UUIDs in matching Stubble
// records and emits deterministic evidence which contains no build-host path.
func TestRecipeSelectionAttributionUsesStubbleJSONSemantics(t *testing.T) {
	root := t.TempDir()
	hwids := filepath.Join(root, "hwids")
	if err := os.Mkdir(hwids, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecipeFixture(t, filepath.Join(hwids, "z-last.json"), `{
  "type": "devicetree",
  "name": "Surface Pro 11 X1P",
  "compatible": "microsoft,denali-x1p",
  "hwids": ["BBBBBBBB-BBBB-5BBB-8BBB-BBBBBBBBBBBB"]
}`)
	writeRecipeFixture(t, filepath.Join(hwids, "a-first.json"), `{
  "type": "devicetree",
  "name": "Surface Pro 11 OLED",
  "compatible": "microsoft,denali-oled",
  "hwids": ["aaaaaaaa-aaaa-5aaa-8aaa-aaaaaaaaaaaa", "11111111-1111-5111-8111-111111111111"]
}`)

	inventoryInput, inventoryOutput, digestOutput, selectionOutput := recipeInventoryFixture(t, root, []recipeDTBFixture{
		{device: "surface-pro-11-x1p-lcd", compatible: "microsoft,denali-x1p"},
		{device: "surface-pro-11-x1e-oled", compatible: "microsoft,denali-oled"},
	})
	runRecipeSelectionScript(t, "embedded", inventoryInput, inventoryOutput, hwids, "-", digestOutput, selectionOutput)

	encoded, err := os.ReadFile(inventoryOutput)
	if err != nil {
		t.Fatal(err)
	}
	var inventory []kernel.DeviceTree
	if err := json.Unmarshal(encoded, &inventory); err != nil {
		t.Fatalf("decode inventory: %v", err)
	}
	if got, want := []string{inventory[0].Device, inventory[1].Device}, []string{"surface-pro-11-x1e-oled", "surface-pro-11-x1p-lcd"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("inventory order = %v, want %v", got, want)
	}
	wantSelectors := []kernel.DeviceTreeSelector{
		{Kind: kernel.DeviceTreeSelectorCompatible, Value: "microsoft,denali-oled"},
		{Kind: kernel.DeviceTreeSelectorHWID, Value: "11111111-1111-5111-8111-111111111111"},
		{Kind: kernel.DeviceTreeSelectorHWID, Value: "aaaaaaaa-aaaa-5aaa-8aaa-aaaaaaaaaaaa"},
	}
	if !reflect.DeepEqual(inventory[0].Selectors, wantSelectors) {
		t.Fatalf("OLED selectors = %#v, want %#v", inventory[0].Selectors, wantSelectors)
	}
	digest, err := os.ReadFile(digestOutput)
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != 64 || strings.Trim(string(digest), "0123456789abcdef") != "" {
		t.Fatalf("database digest = %q", digest)
	}
	selection, err := os.ReadFile(selectionOutput)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(selection), root) || !strings.Contains(string(selection), `"source": "hwids"`) {
		t.Fatalf("selection evidence exposes a host path or omits its source: %s", selection)
	}

	secondInventory := filepath.Join(root, "inventory-second.json")
	secondDigest := filepath.Join(root, "digest-second")
	secondSelection := filepath.Join(root, "selection-second.json")
	runRecipeSelectionScript(t, "embedded", inventoryInput, secondInventory, hwids, "-", secondDigest, secondSelection)
	assertRecipeFilesEqual(t, inventoryOutput, secondInventory)
	assertRecipeFilesEqual(t, digestOutput, secondDigest)
	assertRecipeFilesEqual(t, selectionOutput, secondSelection)
}

// TestRecipeSelectionAttributionFailsClosed rejects an embedded required DTB
// when the installed Stubble input has no matching compatible selector record.
func TestRecipeSelectionAttributionFailsClosed(t *testing.T) {
	root := t.TempDir()
	hwids := filepath.Join(root, "hwids")
	if err := os.Mkdir(hwids, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecipeFixture(t, filepath.Join(hwids, "other.json"), `{
  "type": "devicetree",
  "name": "Another machine",
  "compatible": "vendor,other",
  "hwids": ["aaaaaaaa-aaaa-5aaa-8aaa-aaaaaaaaaaaa"]
}`)
	inventoryInput, inventoryOutput, digestOutput, selectionOutput := recipeInventoryFixture(t, root, []recipeDTBFixture{{
		device: "surface-pro-11-x1e-oled", compatible: "microsoft,denali-oled",
	}})
	output, err := executeRecipeSelectionScript("embedded", inventoryInput, inventoryOutput, hwids, "-", digestOutput, selectionOutput)
	if err == nil || !strings.Contains(string(output), "has no matching Stubble selector record") {
		t.Fatalf("unattributable selection error = %v, output %q", err, output)
	}
}

// TestRecipeSelectionAttributionRejectsSecondaryCompatible proves that a
// shared secondary compatible is not promoted into an arbitrary DTB fallback.
func TestRecipeSelectionAttributionRejectsSecondaryCompatible(t *testing.T) {
	root := t.TempDir()
	hwids := filepath.Join(root, "hwids")
	if err := os.Mkdir(hwids, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecipeFixture(t, filepath.Join(hwids, "generic.json"), `{
  "type": "devicetree",
  "name": "Shared Surface family",
  "compatible": "microsoft,denali",
  "hwids": ["aaaaaaaa-aaaa-5aaa-8aaa-aaaaaaaaaaaa"]
}`)
	inventoryInput, inventoryOutput, digestOutput, selectionOutput := recipeInventoryFixture(t, root, []recipeDTBFixture{{
		device: "surface-pro-11-x1e-oled", compatible: "microsoft,denali-oled\nmicrosoft,denali",
	}})
	output, err := executeRecipeSelectionScript("embedded", inventoryInput, inventoryOutput, hwids, "-", digestOutput, selectionOutput)
	if err == nil || !strings.Contains(string(output), "has no matching Stubble selector record") {
		t.Fatalf("secondary-compatible attribution error = %v, output %q", err, output)
	}
}

// TestRecipeSelectionAttributionAcceptsUsedMachDB proves model routing may
// attribute a compatible only when the generated image declared a machdb input.
func TestRecipeSelectionAttributionAcceptsUsedMachDB(t *testing.T) {
	root := t.TempDir()
	hwids := filepath.Join(root, "hwids")
	if err := os.Mkdir(hwids, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecipeFixture(t, filepath.Join(hwids, "unrelated.json"), `{
  "type": "devicetree",
  "name": "Another machine",
  "compatible": "vendor,other",
  "hwids": ["aaaaaaaa-aaaa-5aaa-8aaa-aaaaaaaaaaaa"]
}`)
	machdb := filepath.Join(root, "machdb.txt")
	writeRecipeFixture(t, machdb, "Model: Surface Pro 11 LCD\nCompatible: microsoft,denali-x1p\n")
	inventoryInput, inventoryOutput, digestOutput, selectionOutput := recipeInventoryFixture(t, root, []recipeDTBFixture{{
		device: "surface-pro-11-x1p-lcd", compatible: "microsoft,denali-x1p",
	}})
	runRecipeSelectionScript(t, "embedded", inventoryInput, inventoryOutput, hwids, machdb, digestOutput, selectionOutput)
	selection, err := os.ReadFile(selectionOutput)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`"source": "machdb"`, `"models": [`, `"Surface Pro 11 LCD"`} {
		if !strings.Contains(string(selection), required) {
			t.Fatalf("machdb selection evidence omits %q: %s", required, selection)
		}
	}
}

// TestRecipeExternalSelectorsExcludeSharedCompatibles proves external platform
// records retain only the explicit unambiguous Surface variant selectors.
func TestRecipeExternalSelectorsExcludeSharedCompatibles(t *testing.T) {
	root := t.TempDir()
	input, output, digest, selection := recipeInventoryFixture(t, root, []recipeDTBFixture{
		{device: "surface-pro-11-x1e-oled", compatible: "microsoft,denali-oled\nmicrosoft,denali"},
		{device: "surface-pro-11-x1p-lcd", compatible: "microsoft,denali-lcd\nmicrosoft,denali"},
	})
	runRecipeSelectionScript(t, "external-required", input, output, "-", "-", digest, selection)
	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var inventory []kernel.DeviceTree
	if err := json.Unmarshal(encoded, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, tree := range inventory {
		if len(tree.Selectors) != 1 || tree.Selectors[0].Value == "microsoft,denali" {
			t.Fatalf("external selectors are ambiguous: %#v", inventory)
		}
	}
}

// TestRecipeRejectsUndeclaredFixedDTBFallback guards the closed inventory from
// Stubble's separate fixed .dtb path, which has no selector attribution here.
func TestRecipeRejectsUndeclaredFixedDTBFallback(t *testing.T) {
	t.Parallel()
	for _, required := range []string{
		`fixed_dtb_sections="$(awk '$2 == ".dtb"`,
		`[ "$fixed_dtb_sections" -ne 0 ]`,
		`fixed .dtb fallback outside the declared selection model`,
	} {
		if !strings.Contains(containerRecipe, required) {
			t.Fatalf("container recipe omits fixed-DTB rejection %q", required)
		}
	}
}

// TestRecipeVerifiesGeneratedKernelHookContract ensures direct dpkg installs
// are supported by inspecting the finished linux-image package, not by
// assuming every downstream kernel tree retains Ubuntu's trigger lifecycle.
func TestRecipeVerifiesGeneratedKernelHookContract(t *testing.T) {
	t.Parallel()
	for _, required := range []string{
		`dpkg-deb --ctrl-tarfile "$selected_package"`,
		`printf 'interest linux-update-%s\n' "$abi"`,
		`cmp -s "$expected_image_triggers" "$image_triggers"`,
		`grep -Fqx "version=$abi" "$image_postinst"`,
		`grep -Fqx 'image_path=/boot/vmlinuz-$version' "$image_postinst"`,
		`grep -Fqx 'if [ "$1" = triggered ]; then' "$image_postinst"`,
		`grep -Fqx '    trigger=/usr/lib/linux/triggers/$version' "$triggered_postinst"`,
		`grep -Fqx $'\tsh "$trigger"' "$triggered_postinst"`,
		`grep -Fqx $'\trm -f "$trigger"' "$triggered_postinst"`,
		`grep -Fqx 'if [ -d /etc/kernel/postinst.d ]; then' "$image_postinst"`,
		`grep -Fqx '    cat - >/usr/lib/linux/triggers/$version <<EOF' "$postinst_hook_command"`,
		`grep -Fqx 'DEB_MAINT_PARAMS="$*" run-parts --report --exit-on-error --arg=$version \' "$postinst_hook_command"`,
		`grep -Fqx '      --arg=$image_path /etc/kernel/postinst.d' "$postinst_hook_command"`,
		`grep -Fqx '    dpkg-trigger --no-await linux-update-$version' "$image_postinst"`,
		`grep -Fqx 'if [ -d /etc/kernel/postrm.d ]; then' "$image_postrm"`,
		`DEB_MAINT_PARAMS="$*" run-parts --report --exit-on-error --arg=$version`,
		`--arg=$image_path /etc/kernel/postrm.d`,
		`does not implement the exact ABI kernel-hook lifecycle`,
		`does not propagate its package lifecycle action to exact-ABI kernel hooks`,
	} {
		if !strings.Contains(containerRecipe, required) {
			t.Fatalf("container recipe omits generated kernel-hook validation %q", required)
		}
	}
}

// recipeDTBFixture supplies one required DTB and matching compatible string.
type recipeDTBFixture struct {
	device     string
	compatible string
}

// recipeInventoryFixture writes bounded inputs and returns the attribution paths.
func recipeInventoryFixture(t *testing.T, root string, devices []recipeDTBFixture) (string, string, string, string) {
	t.Helper()
	input := filepath.Join(root, "inventory.tsv")
	var lines strings.Builder
	for index, device := range devices {
		compatiblePath := filepath.Join(root, device.device+".compatibles")
		writeRecipeFixture(t, compatiblePath, device.compatible+"\nqcom,soc\n")
		lines.WriteString(device.device)
		lines.WriteString("\tdevice-")
		lines.WriteString(string(rune('a' + index)))
		lines.WriteString(".dtb\tusr/lib/firmware/test/device-tree/qcom/device-")
		lines.WriteString(string(rune('a' + index)))
		lines.WriteString(".dtb\t")
		lines.WriteString(strings.Repeat(string(rune('a'+index)), 64))
		lines.WriteString("\t1\t")
		lines.WriteString(compatiblePath)
		lines.WriteString("\ttrue")
		lines.WriteByte('\n')
	}
	writeRecipeFixture(t, input, lines.String())
	return input, filepath.Join(root, "inventory.json"), filepath.Join(root, "hwids.sha256"), filepath.Join(root, "selection.json")
}

// runRecipeSelectionScript requires the embedded attribution program to pass.
func runRecipeSelectionScript(t *testing.T, arguments ...string) {
	t.Helper()
	if output, err := executeRecipeSelectionScript(arguments...); err != nil {
		t.Fatalf("selection attribution failed: %v\n%s", err, output)
	}
}

// executeRecipeSelectionScript runs the exact Python heredoc shipped to Docker.
func executeRecipeSelectionScript(arguments ...string) ([]byte, error) {
	const startMarker = "<<'PY_SELECTION'\n"
	const endMarker = "\nPY_SELECTION\n"
	start := strings.Index(containerRecipe, startMarker)
	if start < 0 {
		return nil, os.ErrNotExist
	}
	start += len(startMarker)
	end := strings.Index(containerRecipe[start:], endMarker)
	if end < 0 {
		return nil, os.ErrInvalid
	}
	command := exec.Command("python3", append([]string{"-"}, arguments...)...)
	command.Stdin = strings.NewReader(containerRecipe[start : start+end])
	return command.CombinedOutput()
}

// recipeTarMember is one synthetic package data-archive member.
type recipeTarMember struct {
	name     string
	data     []byte
	typeflag byte
	linkname string
}

// writeRecipeTar creates a deterministic uncompressed package data archive.
func writeRecipeTar(t *testing.T, path string, members []recipeTarMember) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	archive := tar.NewWriter(file)
	for _, member := range members {
		typeflag := member.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		size := int64(len(member.data))
		if typeflag != tar.TypeReg && typeflag != tar.TypeRegA {
			size = 0
		}
		header := &tar.Header{
			Name: member.name, Mode: 0o644, Size: size, Typeflag: typeflag, Linkname: member.linkname,
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := archive.Write(member.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// executeRecipeDTBScanner runs the exact bounded archive scanner shipped to
// Docker, substituting a test dpkg-deb which exposes synthetic tar archives.
func executeRecipeDTBScanner(t *testing.T, arguments ...string) ([]byte, error) {
	t.Helper()
	return executeRecipeDTBScannerScript(t, recipeDTBScannerScript(t), arguments...)
}

// recipeDTBScannerScript extracts the exact Python scanner embedded in the
// compiled Docker recipe.
func recipeDTBScannerScript(t *testing.T) string {
	t.Helper()
	const startMarker = "<<'PY_DTB_ARCHIVE_SCAN'\n"
	const endMarker = "\nPY_DTB_ARCHIVE_SCAN\n"
	start := strings.Index(containerRecipe, startMarker)
	if start < 0 {
		t.Fatal(os.ErrNotExist)
	}
	start += len(startMarker)
	end := strings.Index(containerRecipe[start:], endMarker)
	if end < 0 {
		t.Fatal(os.ErrInvalid)
	}
	return containerRecipe[start : start+end]
}

// executeRecipeDTBScannerScript runs a supplied scanner program with the
// deterministic test dpkg-deb shim used by archive fixtures.
func executeRecipeDTBScannerScript(t *testing.T, script string, arguments ...string) ([]byte, error) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	dpkgDeb := filepath.Join(bin, "dpkg-deb")
	writeRecipeFixture(t, dpkgDeb, "#!/bin/sh\n[ \"$1\" = --fsys-tarfile ] && [ \"$#\" -eq 2 ] || exit 64\nexec /bin/cat \"$2\"\n")
	if err := os.Chmod(dpkgDeb, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", append([]string{"-"}, arguments...)...)
	command.Stdin = strings.NewReader(script)
	command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return command.CombinedOutput()
}

// writeRecipeFixture creates one test-only regular input file.
func writeRecipeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertRecipeFilesEqual requires two deterministic recipe outputs to match.
func assertRecipeFilesEqual(t *testing.T, first, second string) {
	t.Helper()
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstBytes, secondBytes) {
		t.Fatalf("files differ:\n%s\n%s", firstBytes, secondBytes)
	}
}
