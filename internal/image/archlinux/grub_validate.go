package archlinux

import (
	"archive/tar"
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// validateStandaloneGRUB reads GRUB's ARM64 module table and embedded tar disk.
// The embedded bootstrap must select this image; inspecting an external config
// alone would miss a different bootstrap compiled into the EFI executable.
func validateStandaloneGRUB(filename, bootstrap string) error {
	executable, err := pe.Open(filename)
	if err != nil {
		return err
	}
	defer executable.Close()
	if executable.Machine != pe.IMAGE_FILE_MACHINE_ARM64 {
		return errors.New("GRUB is not an ARM64 EFI executable")
	}
	header, ok := executable.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || header.Subsystem != 10 {
		return errors.New("GRUB is not an EFI application")
	}
	section := executable.Section("mods")
	if section == nil || section.Size > 32<<20 {
		return errors.New("GRUB module section is missing or oversized")
	}
	data, err := section.Data()
	if err != nil {
		return err
	}
	if len(data) < 24 || binary.LittleEndian.Uint32(data) != 0x676d696d {
		return errors.New("GRUB module header is invalid")
	}
	offset := binary.LittleEndian.Uint64(data[8:])
	end := binary.LittleEndian.Uint64(data[16:])
	if offset < 24 || end > uint64(len(data)) || offset >= end {
		return errors.New("GRUB module table bounds are invalid")
	}
	disks, prefixes := 0, 0
	for offset < end {
		if end-offset < 8 {
			return errors.New("truncated GRUB module header")
		}
		kind := binary.LittleEndian.Uint32(data[offset:])
		size := uint64(binary.LittleEndian.Uint32(data[offset+4:]))
		if size < 8 || size > end-offset {
			return errors.New("GRUB module extends outside its section")
		}
		if kind != 0 && kind != 1 && kind != 3 {
			return errors.New("unexpected embedded GRUB object")
		}
		if kind == 3 {
			prefixes++
			prefix := data[offset+8 : offset+size]
			if !bytes.HasPrefix(prefix, []byte("(memdisk)/boot/grub\x00")) || len(bytes.TrimRight(prefix[len("(memdisk)/boot/grub"):], "\x00")) != 0 {
				return errors.New("GRUB prefix does not select its validated memdisk")
			}
		}
		if kind == 1 {
			disks++
			reader := tar.NewReader(bytes.NewReader(data[offset+8 : offset+size]))
			configs := 0
			for {
				header, err := reader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("read GRUB embedded disk: %w", err)
				}
				name := strings.TrimPrefix(header.Name, "./")
				if strings.HasSuffix(name, ".cfg") {
					if name != "boot/grub/grub.cfg" || configs != 0 || header.Typeflag != tar.TypeReg || header.Size > 64<<10 {
						return errors.New("unexpected GRUB memdisk configuration")
					}
					text, err := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
					if err != nil {
						return err
					}
					if string(text) != bootstrap {
						return errors.New("GRUB embedded bootstrap selects a different image")
					}
					configs++
				}
			}
			if configs != 1 {
				return errors.New("GRUB memdisk lacks its sole bootstrap")
			}
		}
		offset += (size + 7) &^ 7
	}
	if offset != end || disks != 1 || prefixes != 1 {
		return errors.New("GRUB requires exactly one complete embedded disk")
	}
	return nil
}
