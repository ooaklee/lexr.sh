package archlinux

import (
	"archive/tar"
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// grubFixture builds only the PE/module-table structures this validator reads.
func grubFixture(t *testing.T, config, prefix string) []byte {
	t.Helper()
	var disk bytes.Buffer
	archive := tar.NewWriter(&disk)
	if err := archive.WriteHeader(&tar.Header{Name: "boot/grub/grub.cfg", Mode: 0644, Size: int64(len(config)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write([]byte(config)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	modules := make([]byte, 24)
	binary.LittleEndian.PutUint32(modules, 0x676d696d)
	binary.LittleEndian.PutUint64(modules[8:], 24)
	for _, part := range []struct {
		kind uint32
		data []byte
	}{{1, disk.Bytes()}, {3, append([]byte(prefix), 0)}} {
		header := make([]byte, 8)
		binary.LittleEndian.PutUint32(header, part.kind)
		binary.LittleEndian.PutUint32(header[4:], uint32(8+len(part.data)))
		modules = append(modules, header...)
		modules = append(modules, part.data...)
		for len(modules)%8 != 0 {
			modules = append(modules, 0)
		}
	}
	binary.LittleEndian.PutUint64(modules[16:], uint64(len(modules)))
	var executable bytes.Buffer
	dos := make([]byte, 64)
	copy(dos, []byte("MZ"))
	binary.LittleEndian.PutUint32(dos[60:], 64)
	executable.Write(dos)
	executable.Write([]byte{'P', 'E', 0, 0})
	for _, record := range []any{pe.FileHeader{Machine: pe.IMAGE_FILE_MACHINE_ARM64, NumberOfSections: 1, SizeOfOptionalHeader: 240}, pe.OptionalHeader64{Magic: 0x20b, Subsystem: 10, NumberOfRvaAndSizes: 16}, pe.SectionHeader32{Name: [8]byte{'m', 'o', 'd', 's'}, SizeOfRawData: uint32(len(modules)), PointerToRawData: 512}} {
		if err := binary.Write(&executable, binary.LittleEndian, record); err != nil {
			t.Fatal(err)
		}
	}
	if executable.Len() > 512 {
		t.Fatal("fixture header exceeded file offset")
	}
	executable.Write(make([]byte, 512-executable.Len()))
	executable.Write(modules)
	return executable.Bytes()
}

// TestGRUBBootstrapContract rejects a different embedded menu, prefix, machine
// type or truncated module table even when a separate ISO menu looks correct.
func TestGRUBBootstrapContract(t *testing.T) {
	boot := LiveBoot{ABI: "7.2.0-jg-0sp11v23-qcom-x1e", ImageID: "0123456789abcdef0123456789abcdef"}
	config, _ := boot.BootstrapConfig()
	cases := []struct {
		name, config, prefix string
		mutate               func([]byte)
		valid                bool
	}{
		{name: "valid", config: config, prefix: "(memdisk)/boot/grub", valid: true},
		{name: "different marker", config: "configfile /other.cfg\n", prefix: "(memdisk)/boot/grub"},
		{name: "different prefix", config: config, prefix: "(hd0)/boot/grub"},
		{name: "wrong architecture", config: config, prefix: "(memdisk)/boot/grub", mutate: func(b []byte) { binary.LittleEndian.PutUint16(b[68:], pe.IMAGE_FILE_MACHINE_AMD64) }},
		{name: "invalid module bounds", config: config, prefix: "(memdisk)/boot/grub", mutate: func(b []byte) { binary.LittleEndian.PutUint64(b[512+16:], 1<<30) }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			data := grubFixture(t, test.config, test.prefix)
			if test.mutate != nil {
				test.mutate(data)
			}
			name := filepath.Join(t.TempDir(), "BOOTAA64.EFI")
			if err := os.WriteFile(name, data, 0644); err != nil {
				t.Fatal(err)
			}
			err := validateStandaloneGRUB(name, config)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v: %v", test.valid, err)
			}
		})
	}
}
