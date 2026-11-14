package install

import (
	"debug/elf"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCompanionModule writes one minimal ELF64 object carrying the requested
// vermagic line in a .modinfo section so companion-module warnings can assert
// against real parsed metadata.
func writeCompanionModule(t *testing.T, path, vermagic string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Reuse a real compiled object with a .modinfo section: build it from the
	// smallest fixture in this package when available, otherwise synthesise a
	// section-only ELF via the debug/elf writer is unavailable, so assemble a
	// hand-rolled little-endian ELF64 with one SHF_STRINGS section.
	section := "vermagic=" + vermagic + "\x00"
	data := companionModuleELF(section)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCompanionModuleWarningListsStaleABIs verifies the preflight warning
// names every out-of-tree companion module whose module tree ABI no longer
// matches the installation target, including parsed vermagic.
func TestCompanionModuleWarningListsStaleABIs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	updates := filepath.Join(root, "usr/lib/modules", fixtureFallbackABI, "updates")
	writeCompanionModule(t, filepath.Join(updates, "ov13858.ko"), fixtureFallbackABI+" SMP preempt mod_unload aarch64")
	writeCompanionModule(t, filepath.Join(updates, "vd55g1.ko"), fixtureFallbackABI+" SMP preempt mod_unload aarch64")

	warning := companionModuleABIWarning(root, fixtureTargetABI)
	if warning == "" {
		t.Fatal("expected a companion module ABI warning for the fallback module tree")
	}
	for _, module := range []string{"ov13858.ko", "vd55g1.ko"} {
		if !strings.Contains(warning, module) {
			t.Errorf("warning does not list %s: %s", module, warning)
		}
	}
	if !strings.Contains(warning, "vermagic "+fixtureFallbackABI) {
		t.Errorf("warning does not disclose the stale vermagic: %s", warning)
	}
	if !strings.Contains(warning, fixtureFallbackABI+": updates/") {
		t.Errorf("warning does not group modules under the stale ABI updates directory: %s", warning)
	}
}

// TestCompanionModuleWarningIgnoresTargetABI verifies modules already rebuilt
// for the installation target never produce a warning.
func TestCompanionModuleWarningIgnoresTargetABI(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	updates := filepath.Join(root, "usr/lib/modules", fixtureTargetABI, "updates")
	writeCompanionModule(t, filepath.Join(updates, "ps883x.ko"), fixtureTargetABI+" SMP preempt mod_unload aarch64")

	if warning := companionModuleABIWarning(root, fixtureTargetABI); warning != "" {
		t.Fatalf("expected no warning for matching target ABI modules, got: %s", warning)
	}
}

// TestCompanionModuleWarningReportsCompressedWithoutVermagic verifies the
// compressed module forms, whose vermagic cannot be read by debug/elf, still
// warn using their module tree ABI.
func TestCompanionModuleWarningReportsCompressedWithoutVermagic(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	updates := filepath.Join(root, "usr/lib/modules", fixtureFallbackABI, "updates")
	if err := os.MkdirAll(updates, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(updates, "imx681.ko.zst"), []byte("compressed module"), 0o644); err != nil {
		t.Fatal(err)
	}

	warning := companionModuleABIWarning(root, fixtureTargetABI)
	if !strings.Contains(warning, "imx681.ko.zst") || !strings.Contains(warning, "vermagic "+fixtureFallbackABI) {
		t.Fatalf("expected the compressed module to warn with its tree ABI, got: %s", warning)
	}
}

// TestCompanionModuleWarningDeduplicatesMergedUsr verifies a merged-usr lib
// symlink onto usr/lib does not double-report the same modules.
func TestCompanionModuleWarningDeduplicatesMergedUsr(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeCompanionModule(t, filepath.Join(root, "usr/lib/modules", fixtureFallbackABI, "updates", "foo.ko"),
		fixtureFallbackABI+" SMP preempt mod_unload aarch64")
	if err := os.Symlink(filepath.Join(root, "usr/lib"), filepath.Join(root, "lib")); err != nil {
		t.Fatal(err)
	}

	warning := companionModuleABIWarning(root, fixtureTargetABI)
	if got := strings.Count(warning, "foo.ko"); got != 1 {
		t.Fatalf("expected foo.ko once after merged-usr de-duplication, got %d occurrences: %s", got, warning)
	}
}

// TestCompanionModuleWarningEmptyRoot verifies a root without out-of-tree
// modules stays silent.
func TestCompanionModuleWarningEmptyRoot(t *testing.T) {
	t.Parallel()
	if warning := companionModuleABIWarning(t.TempDir(), fixtureTargetABI); warning != "" {
		t.Fatalf("expected no warning on a root without companion modules, got: %s", warning)
	}
}

// TestPreflightWarnsForStaleCompanionModules verifies the wired preflight path
// emits the companion warning alongside its plan.
func TestPreflightWarnsForStaleCompanionModules(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	writeCompanionModule(t, filepath.Join(root, "usr/lib/modules", fixtureFallbackABI, "updates", "ov13858.ko"),
		fixtureFallbackABI+" SMP preempt mod_unload aarch64")
	plan, err := fixtureManager(&fakeRunner{root: root}).Preflight(fixtureContext(), fixtureRequest(root, bundle, true))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, warning := range plan.Warnings {
		if strings.Contains(warning, "ov13858.ko") && strings.Contains(warning, "companion out-of-tree modules") {
			found = true
		}
	}
	if !found {
		t.Fatalf("preflight plan did not retain the companion module warning: %v", plan.Warnings)
	}
}

// companionModuleELF assembles the smallest little-endian ELF64 object whose
// single .modinfo section holds the requested NUL-terminated strings.
func companionModuleELF(modinfo string) []byte {
	shstrtab := "\x00.modinfo\x00.shstrtab\x00"
	modinfoName := uint32(1)                    // ".modinfo" after the leading NUL
	shstrtabName := uint32(len(".modinfo") + 2) // ".shstrtab" after NUL+.modinfo+NUL
	dataOffset := uint64(64)
	modinfoOffset := dataOffset
	shstrtabOffset := dataOffset + uint64(len(modinfo))
	shoff := dataOffset + uint64(len(modinfo)+len(shstrtab))

	buffer := make([]byte, shoff+3*64)
	copy(buffer, "\x7fELF\x02\x01\x01\x00")
	put16(buffer, 16, 2) // ET_EXEC
	put16(buffer, 18, uint16(elf.EM_AARCH64))
	put32(buffer, 20, 1) // EV_CURRENT
	put64(buffer, 40, shoff)
	put16(buffer, 52, 64) // e_ehsize
	put16(buffer, 58, 64) // e_shentsize
	put16(buffer, 60, 3)  // e_shnum
	put16(buffer, 62, 2)  // e_shstrndx
	copy(buffer[64:], modinfo)
	copy(buffer[64+len(modinfo):], shstrtab)

	modinfoHeader := buffer[shoff+64:]
	put32(modinfoHeader, 0, 1) // SHT_PROGBITS
	put64(modinfoHeader, 8, modinfoOffset)
	put64(modinfoHeader, 16, uint64(len(modinfo)))
	put32(modinfoHeader, 24, 0x22) // SHF_ALLOC|SHF_STRINGS
	put32(modinfoHeader, 40, modinfoName)

	shstrtabHeader := buffer[shoff+128:]
	put32(shstrtabHeader, 0, 3) // SHT_STRTAB
	put64(shstrtabHeader, 8, shstrtabOffset)
	put64(shstrtabHeader, 16, uint64(len(shstrtab)))
	put32(shstrtabHeader, 40, shstrtabName)
	return buffer
}

// put16 writes one little-endian 16-bit value at the byte offset.
func put16(b []byte, off int, v uint16) { b[off] = byte(v); b[off+1] = byte(v >> 8) }

// put32 writes one little-endian 32-bit value at the byte offset.
func put32(b []byte, off int, v uint32) {
	b[off] = byte(v)
	b[off+1] = byte(v >> 8)
	b[off+2] = byte(v >> 16)
	b[off+3] = byte(v >> 24)
}

// put64 writes one little-endian 64-bit value at the byte offset.
func put64(b []byte, off int, v uint64) {
	for i := 0; i < 8; i++ {
		b[off+i] = byte(v >> (8 * i))
	}
}
