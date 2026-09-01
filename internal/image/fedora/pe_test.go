package fedora

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestMatchingPESectionPayloadsIgnoresFileAlignmentPadding proves the exact
// DTB matcher uses PE virtual size rather than comparing raw alignment bytes.
func TestMatchingPESectionPayloadsIgnoresFileAlignmentPadding(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	payload := []byte("surface-dtb-payload")
	payloadPath := filepath.Join(root, "surface.dtb")
	if err := os.WriteFile(payloadPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	const (
		coffHeaderSize    = 20
		sectionHeaderSize = 40
		rawSectionSize    = 512
	)
	image := make([]byte, coffHeaderSize+sectionHeaderSize+rawSectionSize)
	binary.LittleEndian.PutUint16(image[0:2], 0xaa64) // IMAGE_FILE_MACHINE_ARM64
	binary.LittleEndian.PutUint16(image[2:4], 1)
	section := image[coffHeaderSize : coffHeaderSize+sectionHeaderSize]
	copy(section[:8], ".dtbauto")
	binary.LittleEndian.PutUint32(section[8:12], uint32(len(payload)))
	binary.LittleEndian.PutUint32(section[16:20], rawSectionSize)
	binary.LittleEndian.PutUint32(section[20:24], coffHeaderSize+sectionHeaderSize)
	copy(image[coffHeaderSize+sectionHeaderSize:], payload)
	imagePath := filepath.Join(root, "stubble.efi")
	if err := os.WriteFile(imagePath, image, 0o600); err != nil {
		t.Fatal(err)
	}

	matches, sections, err := matchingPESectionPayloads(imagePath, ".dtbauto", payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	if matches != 1 || sections != 1 {
		t.Fatalf("matchingPESectionPayloads() = (%d, %d), want (1, 1)", matches, sections)
	}
	matches, sections, err = matchingPESectionValues(imagePath, ".dtbauto", payload)
	if err != nil {
		t.Fatal(err)
	}
	if matches != 1 || sections != 1 {
		t.Fatalf("matchingPESectionValues() = (%d, %d), want (1, 1)", matches, sections)
	}
	if err := os.WriteFile(payloadPath, []byte("different-dtb-payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	matches, sections, err = matchingPESectionPayloads(imagePath, ".dtbauto", payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	if matches != 0 || sections != 1 {
		t.Fatalf("mismatched matchingPESectionPayloads() = (%d, %d), want (0, 1)", matches, sections)
	}
	matches, sections, err = matchingPESectionValues(imagePath, ".dtbauto", []byte("different-dtb-payload"))
	if err != nil {
		t.Fatal(err)
	}
	if matches != 0 || sections != 1 {
		t.Fatalf("mismatched matchingPESectionValues() = (%d, %d), want (0, 1)", matches, sections)
	}
}
