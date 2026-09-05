package popos

import (
	"strings"
	"testing"
)

// sourceGRUBFixture preserves the paths and selectors observed in the
// checksum-pinned pop-os-24-04-arm64-generic-3 source image.
const sourceGRUBFixture = `menuentry "Try or Install Pop_OS" --class pop-os {
 linux /casper_pop-os_24.04_arm64_generic_debug_217/vmlinuz.efi boot=casper live-media-path=/casper_pop-os_24.04_arm64_generic_debug_217 hostname=pop-os username=pop-os noprompt quiet splash console=tty0 ast.modeset=0 $extra_kernel_parameters ---
 initrd /casper_pop-os_24.04_arm64_generic_debug_217/initrd.gz
}`

// sourceDiskInfoFixture retains Pop's installer-facing media identity.
const sourceDiskInfoFixture = "Pop_OS 24.04 \"Noble Numbat\" - Release arm64 (20251210)\n\n"

// TestParseSourceLayoutRetainsPopPaths proves the catalogue's generic build
// can retain its differently named internal live directory and installer path.
func TestParseSourceLayoutRetainsPopPaths(t *testing.T) {
	layout, err := parseSourceLayout([]byte(sourceGRUBFixture), []byte(sourceDiskInfoFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := "casper_pop-os_24.04_arm64_generic_debug_217"
	if layout.liveDirectory != want || layout.kernel != want+"/vmlinuz.efi" || layout.initrd != want+"/initrd.gz" || layout.member("filesystem.squashfs") != want+"/filesystem.squashfs" {
		t.Fatalf("unexpected Pop layout: %#v", layout)
	}
}

// TestParseSourceLayoutRejectsMismatchedAndUnsafeMedia prevents a path from
// selecting a different filesystem or escaping the isolated source tree.
func TestParseSourceLayoutRejectsMismatchedAndUnsafeMedia(t *testing.T) {
	for name, config := range map[string]string{
		"missing media path": strings.ReplaceAll(sourceGRUBFixture, "live-media-path=", "ignored="),
		"different media":    strings.ReplaceAll(sourceGRUBFixture, "live-media-path=/casper_pop-os_24.04_arm64_generic_debug_217", "live-media-path=/casper"),
		"duplicate selector": strings.ReplaceAll(sourceGRUBFixture, "boot=casper", "boot=casper boot=local"),
		"multiple initrds":   strings.ReplaceAll(sourceGRUBFixture, "initrd.gz\n", "initrd.gz /other.img\n"),
		"traversal":          strings.ReplaceAll(sourceGRUBFixture, "casper_pop-os_24.04_arm64_generic_debug_217", "casper_pop-os_24.04/../escape"),
		"shell substitution": strings.ReplaceAll(sourceGRUBFixture, "casper_pop-os_24.04_arm64_generic_debug_217", "casper_pop-os_$(command)"),
		"second menu":        sourceGRUBFixture + "\n" + sourceGRUBFixture,
		"unpaired initrd":    strings.ReplaceAll(sourceGRUBFixture, "generic_debug_217/initrd", "generic_debug_218/initrd"),
		"Concept layout":     strings.ReplaceAll(sourceGRUBFixture, "casper_pop-os_24.04_arm64_generic_debug_217", "casper"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSourceLayout([]byte(config), []byte(sourceDiskInfoFixture)); err == nil {
				t.Fatal("accepted unsupported source layout")
			}
		})
	}
	for _, info := range []string{"", strings.ReplaceAll(sourceDiskInfoFixture, "arm64", "amd64"), strings.ReplaceAll(sourceDiskInfoFixture, "Pop_OS", "Ubuntu")} {
		if _, err := parseSourceLayout([]byte(sourceGRUBFixture), []byte(info)); err == nil {
			t.Fatalf("accepted unsupported media identity %q", info)
		}
	}
}
