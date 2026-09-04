package build

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

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
