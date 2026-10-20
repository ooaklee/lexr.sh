package build

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

type nativeELF struct {
	needed  map[string]bool
	exports map[string]map[string]bool
	imports []elf.ImportedSymbol
}

var coreELFNames = []string{"libqmi-glib.so.5.12.0", "libssc.so.2", "ssccli", "iio-sensor-proxy", "monitor-sensor"}

// preflightELF bounds the untrusted ELF tables before debug/elf can allocate
// section or symbol data. Compressed/extended tables are not source outputs in
// this compiled contract; accepting them would introduce decompression limits.
func preflightELF(data []byte) error {
	if len(data) < 64 || !bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'}) || data[4] != 2 || data[5] != 1 || data[6] != 1 {
		return errors.New("artifact must be a little-endian ELF64 object")
	}
	b := binary.LittleEndian
	if b.Uint16(data[18:20]) != uint16(elf.EM_AARCH64) || b.Uint16(data[52:54]) != 64 || b.Uint16(data[54:56]) != 56 || b.Uint16(data[58:60]) != 64 {
		return errors.New("artifact ELF architecture/header sizes differ")
	}
	phoff, shoff := b.Uint64(data[32:40]), b.Uint64(data[40:48])
	phnum, shnum, shstr := uint64(b.Uint16(data[56:58])), uint64(b.Uint16(data[60:62])), uint64(b.Uint16(data[62:64]))
	within := func(offset, size uint64) bool { return offset <= uint64(len(data)) && size <= uint64(len(data))-offset }
	if phnum == 0 || phnum > 512 || shnum == 0 || shnum > 4096 || shstr == 0 || shstr >= shnum || !within(phoff, phnum*56) || !within(shoff, shnum*64) {
		return errors.New("artifact ELF table bounds differ")
	}
	for n := uint64(0); n < phnum; n++ {
		p := data[phoff+n*56 : phoff+(n+1)*56]
		if !within(b.Uint64(p[8:16]), b.Uint64(p[32:40])) {
			return errors.New("artifact ELF program range escapes bytes")
		}
		if elf.ProgType(b.Uint32(p[:4])) == elf.PT_LOAD {
			offset, va, fileSize, memorySize, alignment := b.Uint64(p[8:16]), b.Uint64(p[16:24]), b.Uint64(p[32:40]), b.Uint64(p[40:48]), b.Uint64(p[48:56])
			if fileSize > memorySize || memorySize > ^uint64(0)-va || alignment > 1 && (alignment&(alignment-1) != 0 || offset%alignment != va%alignment) {
				return errors.New("artifact ELF load range/alignment differs")
			}
		}
	}
	for n := uint64(0); n < shnum; n++ {
		s := data[shoff+n*64 : shoff+(n+1)*64]
		typ, flags, offset, size := elf.SectionType(b.Uint32(s[4:8])), b.Uint64(s[8:16]), b.Uint64(s[24:32]), b.Uint64(s[32:40])
		if flags&uint64(elf.SHF_COMPRESSED) != 0 || typ != elf.SHT_NOBITS && !within(offset, size) {
			return errors.New("artifact ELF section is compressed or out of bounds")
		}
		if typ == elf.SHT_DYNSYM && (size > 24*100000 || size%24 != 0 || b.Uint64(s[56:64]) != 24) {
			return errors.New("artifact dynamic symbol table exceeds policy")
		}
	}
	nameHeader := data[shoff+shstr*64 : shoff+(shstr+1)*64]
	if elf.SectionType(b.Uint32(nameHeader[4:8])) != elf.SHT_STRTAB {
		return errors.New("artifact section-name table differs")
	}
	names := data[b.Uint64(nameHeader[24:32]) : b.Uint64(nameHeader[24:32])+b.Uint64(nameHeader[32:40])]
	for n := uint64(0); n < shnum; n++ {
		h := data[shoff+n*64 : shoff+(n+1)*64]
		offset := uint64(b.Uint32(h[:4]))
		if offset >= uint64(len(names)) {
			return errors.New("artifact section name is out of bounds")
		}
		end := bytes.IndexByte(names[offset:], 0)
		if end < 0 || bytes.HasPrefix(names[offset:offset+uint64(end)], []byte(".zdebug")) {
			return errors.New("artifact legacy-compressed/invalid section name")
		}
	}
	return nil
}

func inspectNativeELF(name string, data []byte) (nativeELF, error) {
	result := nativeELF{needed: map[string]bool{}, exports: map[string]map[string]bool{}}
	if err := preflightELF(data); err != nil {
		return result, fmt.Errorf("%s: %w", name, err)
	}
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return result, fmt.Errorf("%s: invalid ELF: %w", name, err)
	}
	defer f.Close()
	isLibrary := name == "libqmi-glib.so.5.12.0" || name == "libssc.so.2"
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Machine != elf.EM_AARCH64 || f.Version != elf.EV_CURRENT || f.Type != elf.ET_DYN && f.Type != elf.ET_EXEC || isLibrary && f.Type != elf.ET_DYN {
		return result, fmt.Errorf("%s: native ELF kind differs", name)
	}
	if f.OSABI != elf.ELFOSABI_NONE && f.OSABI != elf.ELFOSABI_LINUX || f.ABIVersion != 0 {
		return result, fmt.Errorf("%s: ELF OS ABI differs", name)
	}
	interpreters, executableLoads, dynamics := 0, 0, 0
	dynamicSection := f.SectionByType(elf.SHT_DYNAMIC)
	if dynamicSection == nil {
		return result, fmt.Errorf("%s: missing dynamic section", name)
	}
	dynamicSections := 0
	for _, section := range f.Sections {
		if section.Type == elf.SHT_DYNAMIC {
			dynamicSections++
		}
	}
	if dynamicSections != 1 {
		return result, fmt.Errorf("%s: ambiguous dynamic sections", name)
	}
	if err := validateLoaderTables(f, dynamicSection); err != nil {
		return result, fmt.Errorf("%s: %w", name, err)
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Flags&elf.PF_X != 0 {
			executableLoads++
		}
		if p.Type == elf.PT_LOAD && p.Flags&(elf.PF_W|elf.PF_X) == elf.PF_W|elf.PF_X {
			return result, fmt.Errorf("%s: writable executable load", name)
		}
		if p.Type == elf.PT_INTERP {
			interpreters++
			if p.Filesz > 128 {
				return result, fmt.Errorf("%s: oversized interpreter", name)
			}
			interpreter, e := io.ReadAll(p.Open())
			if e != nil || string(interpreter) != "/lib/ld-linux-aarch64.so.1\x00" {
				return result, fmt.Errorf("%s: interpreter differs", name)
			}
		}
		if p.Type == elf.PT_DYNAMIC {
			dynamics++
			if p.Off != dynamicSection.Offset || p.Filesz != dynamicSection.FileSize || p.Vaddr != dynamicSection.Addr {
				return result, fmt.Errorf("%s: loader and section dynamic tables differ", name)
			}
		}
	}
	if executableLoads == 0 || dynamics != 1 || isLibrary && interpreters != 0 || !isLibrary && (interpreters != 1 || f.Entry == 0) {
		return result, fmt.Errorf("%s: executable/interpreter shape differs", name)
	}
	for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH, elf.DT_TEXTREL, elf.DT_AUDIT, elf.DT_DEPAUDIT, elf.DT_FILTER, elf.DT_AUXILIARY} {
		values, e := f.DynValue(tag)
		if e != nil || len(values) != 0 {
			return result, fmt.Errorf("%s: forbidden or malformed dynamic entry %s", name, tag)
		}
	}
	flags, err := f.DynValue(elf.DT_FLAGS)
	if err != nil {
		return result, fmt.Errorf("%s: malformed dynamic flags", name)
	}
	for _, value := range flags {
		if value&uint64(elf.DF_TEXTREL) != 0 {
			return result, fmt.Errorf("%s: dynamic text relocation flag", name)
		}
	}
	sonames, err := f.DynString(elf.DT_SONAME)
	if err != nil {
		return result, fmt.Errorf("%s: invalid SONAME", name)
	}
	expectedSONAME := ""
	if name == "libqmi-glib.so.5.12.0" {
		expectedSONAME = "libqmi-glib.so.5"
	}
	if name == "libssc.so.2" {
		expectedSONAME = "libssc.so.2"
	}
	if expectedSONAME != "" && (len(sonames) != 1 || sonames[0] != expectedSONAME) || expectedSONAME == "" && len(sonames) != 0 {
		return result, fmt.Errorf("%s: SONAME differs", name)
	}
	needed, err := f.DynString(elf.DT_NEEDED)
	if err != nil || len(needed) == 0 || len(needed) > 128 {
		return result, fmt.Errorf("%s: dependency table differs", name)
	}
	for _, dependency := range needed {
		if !safeFlatName(dependency) || result.needed[dependency] {
			return result, fmt.Errorf("%s: duplicate or unsafe dependency", name)
		}
		result.needed[dependency] = true
	}
	if name == "libssc.so.2" && !result.needed["libqmi-glib.so.5"] || (name == "ssccli" || name == "iio-sensor-proxy") && !result.needed["libssc.so.2"] || name == "monitor-sensor" && (result.needed["libssc.so.2"] || result.needed["libqmi-glib.so.5"]) {
		return result, fmt.Errorf("%s: required provider dependency differs", name)
	}
	symbols, err := f.DynamicSymbols()
	if err != nil || len(symbols) == 0 {
		return result, fmt.Errorf("%s: missing dynamic symbols", name)
	}
	publicSSC := sscPublicVersions()
	for _, s := range symbols {
		if s.Name == "" {
			continue
		}
		binding, visibility := elf.ST_BIND(s.Info), elf.ST_VISIBILITY(s.Other)
		if s.Section == elf.SHN_UNDEF {
			if binding == elf.STB_GLOBAL {
				result.imports = append(result.imports, elf.ImportedSymbol{Name: s.Name, Version: s.Version, Library: s.Library})
			}
			continue
		}
		if (binding == elf.STB_GLOBAL || binding == elf.STB_WEAK) && (visibility == elf.STV_DEFAULT || visibility == elf.STV_PROTECTED) {
			if name == "libssc.so.2" {
				_, isFunction := publicSSC[s.Name]
				if isFunction {
					if elf.ST_TYPE(s.Info) != elf.STT_FUNC || int(s.Section) >= len(f.Sections) || f.Sections[s.Section].Flags&elf.SHF_EXECINSTR == 0 || !s.HasVersion || s.VersionIndex.IsHidden() {
						return result, fmt.Errorf("%s: SSC public symbol is not a default defined function", name)
					}
				} else if !sscVersionNode(s.Name) || binding != elf.STB_GLOBAL || elf.ST_TYPE(s.Info) != elf.STT_OBJECT || s.Section != elf.SHN_ABS || s.Size != 0 || s.Value != 0 || s.Library != "" || s.Version != "" && s.Version != s.Name || s.VersionIndex.IsHidden() {
					return result, fmt.Errorf("%s: undeclared/private SSC export %s", name, s.Name)
				}
			}
			versions := result.exports[s.Name]
			if versions == nil {
				versions = map[string]bool{}
				result.exports[s.Name] = versions
			}
			if versions[s.Version] {
				return result, fmt.Errorf("%s: duplicate exported symbol/version", name)
			}
			versions[s.Version] = true
		}
	}
	return result, nil
}

// The loader reads dynamic virtual addresses; debug/elf reads linked sections.
// Bind those selected tables to the same bytes before trusting their agreement.
// This is not a complete ELF relocation/hash-table/code-execution proof.
func validateLoaderTables(f *elf.File, dynamic *elf.Section) error {
	singleSection := func(typ elf.SectionType) (*elf.Section, error) {
		var found *elf.Section
		for _, s := range f.Sections {
			if s.Type == typ {
				if found != nil {
					return nil, errors.New("ambiguous loader metadata sections")
				}
				found = s
			}
		}
		return found, nil
	}
	dynsym, err := singleSection(elf.SHT_DYNSYM)
	if err != nil || dynsym == nil || int(dynsym.Link) >= len(f.Sections) {
		return errors.New("missing/ambiguous dynamic symbols")
	}
	dynstr := f.Sections[dynsym.Link]
	if dynstr.Type != elf.SHT_STRTAB || dynamic.Link != dynsym.Link {
		return errors.New("dynamic string tables differ")
	}
	singleValue := func(tag elf.DynTag) (uint64, error) {
		values, e := f.DynValue(tag)
		if e != nil || len(values) != 1 {
			return 0, fmt.Errorf("dynamic tag %s is absent/ambiguous", tag)
		}
		return values[0], nil
	}
	mapRange := func(va, size uint64) (uint64, error) {
		var offset uint64
		matches := 0
		for _, p := range f.Progs {
			if p.Type != elf.PT_LOAD || p.Flags&elf.PF_R == 0 || va < p.Vaddr {
				continue
			}
			delta := va - p.Vaddr
			if delta <= p.Filesz && size <= p.Filesz-delta {
				offset = p.Off + delta
				matches++
			}
		}
		if matches != 1 {
			return 0, errors.New("loader table has no unique file-backed load mapping")
		}
		return offset, nil
	}
	checkSection := func(s *elf.Section, tag elf.DynTag) error {
		value, e := singleValue(tag)
		if e != nil {
			return e
		}
		offset, e := mapRange(value, s.FileSize)
		if e != nil || value != s.Addr || offset != s.Offset {
			return fmt.Errorf("loader tag %s differs from parsed section bytes", tag)
		}
		return nil
	}
	offset, err := mapRange(dynamic.Addr, dynamic.FileSize)
	if err != nil || offset != dynamic.Offset {
		return errors.New("dynamic section load mapping differs")
	}
	if err := checkSection(dynstr, elf.DT_STRTAB); err != nil {
		return err
	}
	size, err := singleValue(elf.DT_STRSZ)
	if err != nil || size != dynstr.FileSize {
		return errors.New("loader string size differs")
	}
	if err := checkSection(dynsym, elf.DT_SYMTAB); err != nil {
		return err
	}
	size, err = singleValue(elf.DT_SYMENT)
	if err != nil || size != 24 || dynsym.Entsize != 24 {
		return errors.New("loader symbol entry size differs")
	}
	for _, policy := range []struct {
		typ            elf.SectionType
		address, count elf.DynTag
	}{{elf.SHT_GNU_VERSYM, elf.DT_VERSYM, 0}, {elf.SHT_GNU_VERDEF, elf.DT_VERDEF, elf.DT_VERDEFNUM}, {elf.SHT_GNU_VERNEED, elf.DT_VERNEED, elf.DT_VERNEEDNUM}} {
		section, e := singleSection(policy.typ)
		if e != nil {
			return e
		}
		if section == nil || section.FileSize == 0 {
			values, e := f.DynValue(policy.address)
			if e != nil || len(values) != 0 {
				return errors.New("loader references absent version table")
			}
			if policy.count != 0 {
				values, e = f.DynValue(policy.count)
				if e != nil || len(values) != 0 {
					return errors.New("loader references absent version count")
				}
			}
			continue
		}
		if policy.typ == elf.SHT_GNU_VERSYM {
			if section.Link != uint32(indexOfSection(f, dynsym)) || section.FileSize != dynsym.FileSize/24*2 {
				return errors.New("version-symbol section differs")
			}
		} else if section.Link != dynsym.Link {
			return errors.New("version string-table link differs")
		}
		if err := checkSection(section, policy.address); err != nil {
			return err
		}
		if policy.count != 0 {
			count, e := singleValue(policy.count)
			if e != nil || count == 0 || count != uint64(section.Info) {
				return errors.New("loader version count differs")
			}
		}
	}
	return nil
}

func indexOfSection(f *elf.File, wanted *elf.Section) int {
	for i, s := range f.Sections {
		if s == wanted {
			return i
		}
	}
	return -1
}
func sscVersionNode(name string) bool {
	return name == "LIBSSC_0.4.0" || name == "LIBSSC_0.4.5" || name == "LIBSSC_0.4.6" || name == "LIBSSC_0.4.7"
}

func validateNativeELFs(files map[string][]byte) error {
	objects := map[string]nativeELF{}
	for _, name := range coreELFNames {
		object, err := inspectNativeELF(name, files[name])
		if err != nil {
			return err
		}
		objects[name] = object
	}
	ssc := objects["libssc.so.2"]
	expected := sscPublicVersions()
	for name, version := range expected {
		if !ssc.exports[name][version] || len(ssc.exports[name]) != 1 {
			return fmt.Errorf("SSC public export/version missing or changed: %s", name)
		}
	}
	for name := range ssc.exports {
		if _, ok := expected[name]; !ok && !sscVersionNode(name) {
			return fmt.Errorf("SSC exported an undeclared/private symbol: %s", name)
		}
	}
	for _, version := range []string{"LIBSSC_0.4.0", "LIBSSC_0.4.5", "LIBSSC_0.4.6", "LIBSSC_0.4.7"} {
		if len(ssc.exports[version]) != 1 {
			return fmt.Errorf("SSC version node missing: %s", version)
		}
	}
	qmiImports := 0
	for _, name := range []string{"libssc.so.2", "ssccli", "iio-sensor-proxy"} {
		sscImports := 0
		for _, symbol := range objects[name].imports {
			provider, soname := "", ""
			if strings.HasPrefix(symbol.Name, "qmi_") {
				provider = "libqmi-glib.so.5.12.0"
				soname = "libqmi-glib.so.5"
				if name == "libssc.so.2" {
					qmiImports++
				}
			}
			if strings.HasPrefix(symbol.Name, "ssc_") {
				provider = "libssc.so.2"
				soname = "libssc.so.2"
				sscImports++
			}
			if provider != "" && (symbol.Library != soname || symbol.Version == "" || !objects[provider].exports[symbol.Name][symbol.Version]) {
				return fmt.Errorf("%s: required provider import is unsatisfied: %s@%s", name, symbol.Name, symbol.Version)
			}
		}
		if (name == "ssccli" || name == "iio-sensor-proxy") && sscImports == 0 {
			return fmt.Errorf("%s: no SSC imports", name)
		}
	}
	if qmiImports == 0 {
		return errors.New("SSC has no QMI imports")
	}
	return nil
}

// This closed public ABI is from the authenticated 649cc875 libssc.map, not
// from whichever symbols happen to be present in an output library.
func sscPublicVersions() map[string]string {
	groups := map[string]string{
		"LIBSSC_0.4.0": `ssc_sensor_accelerometer_new ssc_sensor_accelerometer_new_finish ssc_sensor_accelerometer_new_sync ssc_sensor_accelerometer_open ssc_sensor_accelerometer_open_finish ssc_sensor_accelerometer_open_sync ssc_sensor_accelerometer_close ssc_sensor_accelerometer_close_finish ssc_sensor_accelerometer_close_sync
ssc_sensor_compass_new ssc_sensor_compass_new_finish ssc_sensor_compass_new_sync ssc_sensor_compass_open ssc_sensor_compass_open_finish ssc_sensor_compass_open_sync ssc_sensor_compass_close ssc_sensor_compass_close_finish ssc_sensor_compass_close_sync
ssc_sensor_gyroscope_new ssc_sensor_gyroscope_new_finish ssc_sensor_gyroscope_new_sync ssc_sensor_gyroscope_open ssc_sensor_gyroscope_open_finish ssc_sensor_gyroscope_open_sync ssc_sensor_gyroscope_close ssc_sensor_gyroscope_close_finish ssc_sensor_gyroscope_close_sync
ssc_sensor_light_new ssc_sensor_light_new_finish ssc_sensor_light_new_sync ssc_sensor_light_open ssc_sensor_light_open_finish ssc_sensor_light_open_sync ssc_sensor_light_close ssc_sensor_light_close_finish ssc_sensor_light_close_sync
ssc_sensor_magnetometer_new ssc_sensor_magnetometer_new_finish ssc_sensor_magnetometer_new_sync ssc_sensor_magnetometer_open ssc_sensor_magnetometer_open_finish ssc_sensor_magnetometer_open_sync ssc_sensor_magnetometer_close ssc_sensor_magnetometer_close_finish ssc_sensor_magnetometer_close_sync
ssc_sensor_proximity_new ssc_sensor_proximity_new_finish ssc_sensor_proximity_new_sync ssc_sensor_proximity_open ssc_sensor_proximity_open_finish ssc_sensor_proximity_open_sync ssc_sensor_proximity_close ssc_sensor_proximity_close_finish ssc_sensor_proximity_close_sync
ssc_sensor_new ssc_sensor_new_finish ssc_sensor_open ssc_sensor_open_finish ssc_sensor_close ssc_sensor_close_finish ssc_sensor_get_type ssc_sensor_accelerometer_get_type ssc_sensor_compass_get_type ssc_sensor_gyroscope_get_type ssc_sensor_light_get_type ssc_sensor_magnetometer_get_type ssc_sensor_proximity_get_type ssc_sensor_error_quark`,
		"LIBSSC_0.4.5": `ssc_sensor_retirement_receipt_get_type ssc_sensor_retirement_receipt_copy ssc_sensor_retirement_receipt_free ssc_sensor_retirement_receipt_get_local_complete ssc_sensor_retirement_receipt_dup_release_error ssc_sensor_retirement_receipt_dup_close_error ssc_sensor_retire_async ssc_sensor_retire_finish`,
		"LIBSSC_0.4.6": `ssc_sensor_uid_list_get_type ssc_sensor_uid_list_copy ssc_sensor_uid_list_free ssc_sensor_uid_list_get_length ssc_sensor_uid_list_get_uid ssc_sensor_discover_async ssc_sensor_discover_finish`,
		"LIBSSC_0.4.7": `ssc_sensor_sample_get_type ssc_sensor_sample_copy ssc_sensor_sample_free ssc_sensor_sample_get_kind ssc_sensor_sample_get_uid_high ssc_sensor_sample_get_uid_low ssc_sensor_sample_get_epoch ssc_sensor_sample_get_sequence ssc_sensor_sample_get_raw_timestamp ssc_sensor_sample_get_accuracy ssc_sensor_sample_get_flags ssc_sensor_sample_get_vector ssc_sensor_sample_get_scalar ssc_sensor_sample_get_near`,
	}
	result := map[string]string{}
	for version, names := range groups {
		for _, name := range strings.Fields(names) {
			result[name] = version
		}
	}
	return result
}
