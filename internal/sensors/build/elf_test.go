package build

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"sort"
	"strings"
	"testing"
)

// These are serialised ELF metadata fixtures, never executable/native compiler
// evidence. They exercise debug/elf and the actual production policy together.
type fixtureSymbol struct{ name, version, library string }

// fixtureELFSpec describes serialised metadata, including reserved unversioned
// indices independently of named GNU version definitions and requirements.
type fixtureELFSpec struct {
	exports                map[string]string
	imports                []fixtureSymbol
	needed                 []string
	soname                 string
	interpreter            string
	extraTag               elf.DynTag
	extraValue             string
	machine                elf.Machine
	osabi                  byte
	hiddenSymbol           string
	objectSymbol           string
	weakSymbol             string
	localSymbol            string
	privateSymbol          string
	undefinedSymbol        string
	nonExecutableSymbol    string
	unversionedImportIndex uint16
	omitVersym             bool
	versionIndices         map[string]uint16
}

// fixtureELF serialises bounded, nonexecuted ELF metadata for the real decoder.
func fixtureELF(t *testing.T, name string, mutate func(*fixtureELFSpec)) []byte {
	t.Helper()
	s := fixtureELFSpec{exports: map[string]string{}, needed: []string{"libc.so.6"}, machine: elf.EM_AARCH64}
	switch name {
	case "libqmi-glib.so.5.12.0":
		s.soname = "libqmi-glib.so.5"
		s.exports["qmi_device_new"] = "LIBQMI_GLIB_1.0"
	case "libssc.so.2":
		s.soname = "libssc.so.2"
		s.exports = sscPublicVersions()
		s.imports = []fixtureSymbol{{"qmi_device_new", "LIBQMI_GLIB_1.0", "libqmi-glib.so.5"}}
		s.needed = append(s.needed, "libqmi-glib.so.5")
	case "ssccli", "iio-sensor-proxy":
		s.interpreter = "/lib/ld-linux-aarch64.so.1\x00"
		s.imports = []fixtureSymbol{{"ssc_sensor_sample_get_type", "LIBSSC_0.4.7", "libssc.so.2"}}
		s.needed = append(s.needed, "libssc.so.2")
	case "monitor-sensor":
		s.interpreter = "/lib/ld-linux-aarch64.so.1\x00"
		s.imports = []fixtureSymbol{{"g_main_loop_new", "GLIB_2.0", "libglib-2.0.so.0"}}
		s.needed = append(s.needed, "libglib-2.0.so.0")
	default:
		t.Fatal("unknown fixture ELF")
	}
	if mutate != nil {
		mutate(&s)
	}
	if name == "libssc.so.2" {
		for _, version := range []string{"LIBSSC_0.4.0", "LIBSSC_0.4.5", "LIBSSC_0.4.6", "LIBSSC_0.4.7"} {
			s.exports[version] = version
		}
	}
	bo := binary.LittleEndian
	dynstr := []byte{0}
	offsets := map[string]uint32{"": 0}
	add := func(value string) uint32 {
		if n, ok := offsets[value]; ok {
			return n
		}
		n := uint32(len(dynstr))
		offsets[value] = n
		dynstr = append(dynstr, []byte(value)...)
		dynstr = append(dynstr, 0)
		return n
	}
	var names []string
	for symbol := range s.exports {
		names = append(names, symbol)
	}
	sort.Strings(names)
	var versions []string
	versionSet := map[string]bool{}
	for _, name := range names {
		v := s.exports[name]
		if v != "" && !versionSet[v] {
			versionSet[v] = true
			versions = append(versions, v)
		}
	}
	sort.Strings(versions)
	versionIDs := map[string]uint16{}
	for i, version := range versions {
		versionIDs[version] = uint16(i + 2)
		add(version)
	}
	for _, name := range names {
		add(name)
	}
	for _, symbol := range s.imports {
		add(symbol.name)
		add(symbol.version)
		add(symbol.library)
	}
	for _, name := range s.needed {
		add(name)
	}
	add(s.soname)
	add(s.extraValue)
	var defs bytes.Buffer
	for i, version := range versions {
		entry := make([]byte, 28)
		bo.PutUint16(entry[0:], 1)
		bo.PutUint16(entry[4:], versionIDs[version])
		bo.PutUint16(entry[6:], 1)
		bo.PutUint32(entry[12:], 20)
		if i+1 < len(versions) {
			bo.PutUint32(entry[16:], 28)
		}
		bo.PutUint32(entry[20:], add(version))
		defs.Write(entry)
	}
	var importVersions []fixtureSymbol
	importIDs := map[string]uint16{}
	for _, symbol := range s.imports {
		if symbol.version == "" {
			continue
		}
		key := symbol.library + "\x00" + symbol.version
		if _, ok := importIDs[key]; !ok {
			importIDs[key] = uint16(10 + len(importVersions))
			importVersions = append(importVersions, symbol)
		}
	}
	var needs bytes.Buffer
	for i, symbol := range importVersions {
		entry := make([]byte, 32)
		bo.PutUint16(entry[0:], 1)
		bo.PutUint16(entry[2:], 1)
		bo.PutUint32(entry[4:], add(symbol.library))
		bo.PutUint32(entry[8:], 16)
		if i+1 < len(importVersions) {
			bo.PutUint32(entry[12:], 32)
		}
		bo.PutUint16(entry[22:], importIDs[symbol.library+"\x00"+symbol.version])
		bo.PutUint32(entry[24:], add(symbol.version))
		needs.Write(entry)
	}
	var symbols, versym bytes.Buffer
	symbols.Write(make([]byte, 24))
	versym.Write([]byte{0, 0})
	writeSymbol := func(name, version, library string, defined bool) {
		entry := make([]byte, 24)
		bo.PutUint32(entry[0:], add(name))
		entry[4] = byte(elf.STB_GLOBAL)<<4 | byte(elf.STT_FUNC)
		if name == s.objectSymbol {
			entry[4] = byte(elf.STB_GLOBAL)<<4 | byte(elf.STT_OBJECT)
		}
		if name == s.weakSymbol {
			entry[4] = byte(elf.STB_WEAK)<<4 | byte(elf.ST_TYPE(entry[4]))
		}
		if name == s.localSymbol {
			entry[4] = byte(elf.STB_LOCAL)<<4 | byte(elf.ST_TYPE(entry[4]))
		}
		if name == s.privateSymbol {
			entry[5] = byte(elf.STV_HIDDEN)
		}
		if name == s.undefinedSymbol {
			defined = false
		}
		index := uint16(0)
		if defined {
			if name != "" && sscVersionNode(name) {
				entry[4] = byte(elf.STB_GLOBAL)<<4 | byte(elf.STT_OBJECT)
				bo.PutUint16(entry[6:], uint16(elf.SHN_ABS))
			} else {
				bo.PutUint16(entry[6:], 1)
				if name == s.nonExecutableSymbol {
					bo.PutUint16(entry[6:], 2)
				}
				bo.PutUint64(entry[8:], 0x400100)
				bo.PutUint64(entry[16:], 4)
			}
			index = 1
			if version != "" {
				index = versionIDs[version]
			}
			if name == s.hiddenSymbol {
				index |= 0x8000
			}
		} else {
			index = s.unversionedImportIndex
			if version != "" {
				index = importIDs[library+"\x00"+version]
			}
		}
		if raw, ok := s.versionIndices[name]; ok {
			index = raw
		}
		symbols.Write(entry)
		v := make([]byte, 2)
		bo.PutUint16(v, index)
		versym.Write(v)
	}
	for _, name := range names {
		writeSymbol(name, s.exports[name], "", true)
	}
	for _, symbol := range s.imports {
		writeSymbol(symbol.name, symbol.version, symbol.library, false)
	}
	var dynamic bytes.Buffer
	writeDynamic := func(tag elf.DynTag, value uint64) {
		entry := make([]byte, 16)
		bo.PutUint64(entry, uint64(tag))
		bo.PutUint64(entry[8:], value)
		dynamic.Write(entry)
	}
	for _, name := range s.needed {
		writeDynamic(elf.DT_NEEDED, uint64(add(name)))
	}
	if s.soname != "" {
		writeDynamic(elf.DT_SONAME, uint64(add(s.soname)))
	}
	if s.extraTag != 0 {
		writeDynamic(s.extraTag, uint64(add(s.extraValue)))
	}
	writeDynamic(elf.DT_STRTAB, 0)
	writeDynamic(elf.DT_STRSZ, uint64(len(dynstr)))
	writeDynamic(elf.DT_SYMTAB, 0)
	writeDynamic(elf.DT_SYMENT, 24)
	if !s.omitVersym {
		writeDynamic(elf.DT_VERSYM, 0)
	}
	if len(versions) > 0 {
		writeDynamic(elf.DT_VERDEF, 0)
		writeDynamic(elf.DT_VERDEFNUM, uint64(len(versions)))
	}
	if len(importVersions) > 0 {
		writeDynamic(elf.DT_VERNEED, 0)
		writeDynamic(elf.DT_VERNEEDNUM, uint64(len(importVersions)))
	}
	writeDynamic(elf.DT_NULL, 0)
	type section struct {
		name      string
		typ       elf.SectionType
		flags     uint64
		data      []byte
		link      uint32
		info      uint32
		entsize   uint64
		offset    uint64
		nameIndex uint32
	}
	sections := []section{{}, {name: ".text", typ: elf.SHT_PROGBITS, flags: uint64(elf.SHF_ALLOC | elf.SHF_EXECINSTR), data: []byte{0xc0, 0x03, 0x5f, 0xd6}}, {name: ".dynstr", typ: elf.SHT_STRTAB, flags: uint64(elf.SHF_ALLOC), data: dynstr}, {name: ".dynsym", typ: elf.SHT_DYNSYM, flags: uint64(elf.SHF_ALLOC), data: symbols.Bytes(), link: 2, entsize: 24}, {name: ".gnu.version", typ: elf.SHT_GNU_VERSYM, flags: uint64(elf.SHF_ALLOC), data: versym.Bytes(), link: 3, entsize: 2}, {name: ".gnu.version_d", typ: elf.SHT_GNU_VERDEF, flags: uint64(elf.SHF_ALLOC), data: defs.Bytes(), link: 2, info: uint32(len(versions))}, {name: ".gnu.version_r", typ: elf.SHT_GNU_VERNEED, flags: uint64(elf.SHF_ALLOC), data: needs.Bytes(), link: 2, info: uint32(len(importVersions))}, {name: ".dynamic", typ: elf.SHT_DYNAMIC, flags: uint64(elf.SHF_ALLOC), data: dynamic.Bytes(), link: 2, entsize: 16}, {name: ".shstrtab", typ: elf.SHT_STRTAB}}
	// Keep stable fixture section indices, but do not invent empty GNU tables.
	if s.omitVersym {
		sections[4] = section{}
	}
	if len(versions) == 0 {
		sections[5] = section{}
	}
	if len(importVersions) == 0 {
		sections[6] = section{}
	}
	shstr := []byte{0}
	for i := 1; i < len(sections); i++ {
		sections[i].nameIndex = uint32(len(shstr))
		shstr = append(shstr, []byte(sections[i].name)...)
		shstr = append(shstr, 0)
	}
	sections[8].data = shstr
	phnum := 2
	if s.interpreter != "" {
		phnum = 3
	}
	data := make([]byte, 64+phnum*56)
	interpOffset := len(data)
	data = append(data, []byte(s.interpreter)...)
	for i := 1; i < len(sections); i++ {
		for len(data)%8 != 0 {
			data = append(data, 0)
		}
		sections[i].offset = uint64(len(data))
		data = append(data, sections[i].data...)
	}
	for len(data)%8 != 0 {
		data = append(data, 0)
	}
	shoff := len(data)
	data = append(data, make([]byte, len(sections)*64)...)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, s.osabi})
	bo.PutUint16(data[16:], uint16(elf.ET_DYN))
	bo.PutUint16(data[18:], uint16(s.machine))
	bo.PutUint32(data[20:], 1)
	if s.interpreter != "" {
		bo.PutUint64(data[24:], 0x400000+sections[1].offset)
	}
	bo.PutUint64(data[32:], 64)
	bo.PutUint64(data[40:], uint64(shoff))
	bo.PutUint16(data[52:], 64)
	bo.PutUint16(data[54:], 56)
	bo.PutUint16(data[56:], uint16(phnum))
	bo.PutUint16(data[58:], 64)
	bo.PutUint16(data[60:], uint16(len(sections)))
	bo.PutUint16(data[62:], 8)
	p := data[64:120]
	bo.PutUint32(p, uint32(elf.PT_LOAD))
	bo.PutUint32(p[4:], uint32(elf.PF_R|elf.PF_X))
	bo.PutUint64(p[16:], 0x400000)
	bo.PutUint64(p[24:], 0x400000)
	bo.PutUint64(p[32:], uint64(len(data)))
	bo.PutUint64(p[40:], uint64(len(data)))
	bo.PutUint64(p[48:], 0x1000)
	if phnum == 3 {
		p = data[120:176]
		bo.PutUint32(p, uint32(elf.PT_INTERP))
		bo.PutUint32(p[4:], uint32(elf.PF_R))
		bo.PutUint64(p[8:], uint64(interpOffset))
		bo.PutUint64(p[16:], 0x400000+uint64(interpOffset))
		bo.PutUint64(p[32:], uint64(len(s.interpreter)))
		bo.PutUint64(p[40:], uint64(len(s.interpreter)))
		bo.PutUint64(p[48:], 1)
	}
	p = data[64+(phnum-1)*56 : 64+phnum*56]
	bo.PutUint32(p, uint32(elf.PT_DYNAMIC))
	bo.PutUint32(p[4:], uint32(elf.PF_R))
	bo.PutUint64(p[8:], sections[7].offset)
	bo.PutUint64(p[16:], 0x400000+sections[7].offset)
	bo.PutUint64(p[32:], uint64(len(sections[7].data)))
	bo.PutUint64(p[40:], uint64(len(sections[7].data)))
	bo.PutUint64(p[48:], 8)
	for i := 0; i < len(names); i++ {
		if !sscVersionNode(names[i]) && names[i] != s.undefinedSymbol {
			bo.PutUint64(data[sections[3].offset+uint64(i+1)*24+8:], 0x400000+sections[1].offset)
		}
	}
	for n := uint64(0); n < uint64(len(sections[7].data)); n += 16 {
		entry := data[sections[7].offset+n : sections[7].offset+n+16]
		tag := elf.DynTag(bo.Uint64(entry))
		section := 0
		switch tag {
		case elf.DT_STRTAB:
			section = 2
		case elf.DT_SYMTAB:
			section = 3
		case elf.DT_VERSYM:
			section = 4
		case elf.DT_VERDEF:
			section = 5
		case elf.DT_VERNEED:
			section = 6
		}
		if section != 0 {
			bo.PutUint64(entry[8:], 0x400000+sections[section].offset)
		}
	}
	for i, section := range sections {
		h := data[shoff+i*64 : shoff+(i+1)*64]
		bo.PutUint32(h, section.nameIndex)
		bo.PutUint32(h[4:], uint32(section.typ))
		bo.PutUint64(h[8:], section.flags)
		if section.flags&uint64(elf.SHF_ALLOC) != 0 {
			bo.PutUint64(h[16:], 0x400000+section.offset)
		}
		bo.PutUint64(h[24:], section.offset)
		bo.PutUint64(h[32:], uint64(len(section.data)))
		bo.PutUint32(h[40:], section.link)
		bo.PutUint32(h[44:], section.info)
		bo.PutUint64(h[48:], 8)
		bo.PutUint64(h[56:], section.entsize)
	}
	return data
}

// fixtureELFs builds the complete nonexecuted ELF metadata roster for the production verifier.
func fixtureELFs(t *testing.T) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	for _, name := range coreELFNames {
		result[name] = fixtureELF(t, name, nil)
	}
	return result
}

// TestNativeELFPolicyAcceptsSerializedMetadata accepts the complete bounded metadata fixture set.
func TestNativeELFPolicyAcceptsSerializedMetadata(t *testing.T) {
	if got := len(sscPublicVersions()); got != 97 {
		t.Fatalf("public ABI count=%d", got)
	}
	if err := validateNativeELFs(fixtureELFs(t)); err != nil {
		t.Fatal(err)
	}
}

// TestNativeELFPolicyRefusesChangedArchitectureAndProviders rejects incompatible ELF and provider identities.
func TestNativeELFPolicyRefusesChangedArchitectureAndProviders(t *testing.T) {
	cases := []struct {
		name, artifact string
		mutate         func(*fixtureELFSpec)
	}{
		{"machine", "ssccli", func(s *fixtureELFSpec) { s.machine = elf.EM_X86_64 }},
		{"osabi", "ssccli", func(s *fixtureELFSpec) { s.osabi = byte(elf.ELFOSABI_FREEBSD) }},
		{"interpreter", "ssccli", func(s *fixtureELFSpec) { s.interpreter = "/wrong/loader\x00" }},
		{"missing-interpreter", "ssccli", func(s *fixtureELFSpec) { s.interpreter = "" }},
		{"library-interpreter", "libssc.so.2", func(s *fixtureELFSpec) { s.interpreter = "/lib/ld-linux-aarch64.so.1\x00" }},
		{"soname", "libssc.so.2", func(s *fixtureELFSpec) { s.soname = "libssc.so.1" }},
		{"needed", "libssc.so.2", func(s *fixtureELFSpec) { s.needed = []string{"libc.so.6"} }},
		{"duplicate-needed", "libssc.so.2", func(s *fixtureELFSpec) { s.needed = append(s.needed, "libqmi-glib.so.5") }},
		{"unsafe-needed", "libssc.so.2", func(s *fixtureELFSpec) { s.needed = append(s.needed, "/tmp/libother.so") }},
		{"monitor-ssc", "monitor-sensor", func(s *fixtureELFSpec) { s.needed = append(s.needed, "libssc.so.2") }},
		{"rpath", "ssccli", func(s *fixtureELFSpec) { s.extraTag = elf.DT_RPATH; s.extraValue = "$ORIGIN" }},
		{"empty-runpath", "ssccli", func(s *fixtureELFSpec) { s.extraTag = elf.DT_RUNPATH }},
		{"audit", "ssccli", func(s *fixtureELFSpec) { s.extraTag = elf.DT_AUDIT; s.extraValue = "other.so" }},
		{"missing-sample", "libssc.so.2", func(s *fixtureELFSpec) { delete(s.exports, "ssc_sensor_sample_copy") }},
		{"changed-sample-version", "libssc.so.2", func(s *fixtureELFSpec) { s.exports["ssc_sensor_sample_copy"] = "LIBSSC_0.4.6" }},
		{"private-client", "libssc.so.2", func(s *fixtureELFSpec) { s.exports["ssc_client_private_open"] = "LIBSSC_0.4.7" }},
		{"private-other-prefix", "libssc.so.2", func(s *fixtureELFSpec) { s.exports["libssc_client_secret"] = "LIBSSC_0.4.7" }},
		{"hidden-public", "libssc.so.2", func(s *fixtureELFSpec) { s.hiddenSymbol = "ssc_sensor_sample_copy" }},
		{"hidden-version-node", "libssc.so.2", func(s *fixtureELFSpec) { s.hiddenSymbol = "LIBSSC_0.4.7" }},
		{"object-public", "libssc.so.2", func(s *fixtureELFSpec) { s.objectSymbol = "ssc_sensor_sample_copy" }},
		{"qmi-version", "libssc.so.2", func(s *fixtureELFSpec) { s.imports[0].version = "LIBQMI_GLIB_FUTURE" }},
		{"qmi-library", "libssc.so.2", func(s *fixtureELFSpec) { s.imports[0].library = "libqmi-glib.so.4" }},
		{"missing-qmi-symbol", "libqmi-glib.so.5.12.0", func(s *fixtureELFSpec) {
			delete(s.exports, "qmi_device_new")
			s.exports["qmi_wrong"] = "LIBQMI_GLIB_1.0"
		}},
		{"ssc-import-version", "ssccli", func(s *fixtureELFSpec) { s.imports[0].version = "LIBSSC_0.4.6" }},
		{"no-ssc-imports", "iio-sensor-proxy", func(s *fixtureELFSpec) {
			s.imports = []fixtureSymbol{{"g_main_loop_new", "GLIB_2.0", "libglib-2.0.so.0"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := fixtureELFs(t)
			files[tc.artifact] = fixtureELF(t, tc.artifact, tc.mutate)
			if err := validateNativeELFs(files); err == nil {
				t.Fatal("accepted changed ELF/provider")
			}
		})
	}
}

// TestNativeELFPreflightBoundsAndCompression refuses malformed geometry and unsupported compressed sections.
func TestNativeELFPreflightBoundsAndCompression(t *testing.T) {
	good := fixtureELF(t, "ssccli", nil)
	for _, kind := range []string{"magic", "class", "endian", "truncated", "section-offset", "section-count", "compressed", "legacy-compressed", "symbol-count", "writable-executable", "zero-entry", "dynamic-mismatch"} {
		t.Run(kind, func(t *testing.T) {
			data := bytes.Clone(good)
			bo := binary.LittleEndian
			shoff := bo.Uint64(data[40:])
			switch kind {
			case "magic":
				data[0] = 0
			case "class":
				data[4] = 1
			case "endian":
				data[5] = 2
			case "truncated":
				data = data[:40]
			case "section-offset":
				bo.PutUint64(data[40:], ^uint64(0))
			case "section-count":
				bo.PutUint16(data[60:], 65535)
			case "compressed":
				bo.PutUint64(data[shoff+64+8:], uint64(elf.SHF_COMPRESSED))
			case "legacy-compressed":
				data = bytes.ReplaceAll(data, []byte(".dynsym"), []byte(".zdebug"))
			case "symbol-count":
				bo.PutUint64(data[shoff+3*64+32:], 24*100001)
			case "writable-executable":
				bo.PutUint32(data[64+4:], uint32(elf.PF_R|elf.PF_W|elf.PF_X))
			case "zero-entry":
				bo.PutUint64(data[24:], 0)
			case "dynamic-mismatch":
				bo.PutUint64(data[64+2*56+8:], 0)
			}
			if _, err := inspectNativeELF("ssccli", data); err == nil {
				t.Fatal("accepted malformed ELF")
			}
		})
	}
}

// TestNativeELFABIIsClosedAndDefensive keeps the required SSC ABI exact and protected from caller mutation.
func TestNativeELFABIIsClosedAndDefensive(t *testing.T) {
	versions := sscPublicVersions()
	delete(versions, "ssc_sensor_sample_copy")
	if len(sscPublicVersions()) != 97 {
		t.Fatal("caller mutated compiled ABI")
	}
	for name, version := range sscPublicVersions() {
		if strings.HasPrefix(name, "ssc_sensor_sample_") && version != "LIBSSC_0.4.7" {
			t.Fatal("sample ABI version differs")
		}
	}
}

// TestNativeELFLoaderAddressesMatchParsedMetadata binds dynamic loader addresses to their parsed tables.
func TestNativeELFLoaderAddressesMatchParsedMetadata(t *testing.T) {
	for _, kind := range []string{"string-address", "string-size", "symbol-address", "symbol-size", "version-address", "definition-count", "need-address", "duplicate-string-address", "load-filesize", "load-alignment", "dynamic-link", "symbol-link", "version-link"} {
		t.Run(kind, func(t *testing.T) {
			data := fixtureELF(t, "ssccli", nil)
			bo := binary.LittleEndian
			shoff := bo.Uint64(data[40:])
			dynamicOffset := bo.Uint64(data[shoff+7*64+24:])
			dynamicSize := bo.Uint64(data[shoff+7*64+32:])
			changeTag := func(tag elf.DynTag, delta uint64) {
				found := false
				for n := uint64(0); n < dynamicSize; n += 16 {
					entry := data[dynamicOffset+n : dynamicOffset+n+16]
					if elf.DynTag(bo.Uint64(entry)) == tag {
						bo.PutUint64(entry[8:], bo.Uint64(entry[8:])+delta)
						found = true
					}
				}
				if !found {
					t.Fatalf("fixture lacks tag %v", tag)
				}
			}
			switch kind {
			case "string-address":
				changeTag(elf.DT_STRTAB, 1)
			case "string-size":
				changeTag(elf.DT_STRSZ, 1)
			case "symbol-address":
				changeTag(elf.DT_SYMTAB, 24)
			case "symbol-size":
				changeTag(elf.DT_SYMENT, 1)
			case "version-address":
				changeTag(elf.DT_VERSYM, 2)
			case "definition-count":
				data = fixtureELF(t, "libssc.so.2", nil)
				shoff = bo.Uint64(data[40:])
				dynamicOffset = bo.Uint64(data[shoff+7*64+24:])
				dynamicSize = bo.Uint64(data[shoff+7*64+32:])
				changeTag(elf.DT_VERDEFNUM, 1)
			case "need-address":
				changeTag(elf.DT_VERNEED, 16)
			case "duplicate-string-address":
				entry := data[dynamicOffset+dynamicSize-16 : dynamicOffset+dynamicSize]
				bo.PutUint64(entry, uint64(elf.DT_STRTAB))
				bo.PutUint64(entry[8:], 0x400000+bo.Uint64(data[shoff+2*64+24:]))
			case "load-filesize":
				bo.PutUint64(data[64+40:], 1)
			case "load-alignment":
				bo.PutUint64(data[64+16:], 0x400001)
			case "dynamic-link":
				bo.PutUint32(data[shoff+7*64+40:], 8)
			case "symbol-link":
				bo.PutUint32(data[shoff+3*64+40:], 8)
			case "version-link":
				bo.PutUint32(data[shoff+4*64+40:], 8)
			}
			name := "ssccli"
			if kind == "definition-count" {
				name = "libssc.so.2"
			}
			if _, err := inspectNativeELF(name, data); err == nil {
				t.Fatal("accepted divergent loader metadata")
			}
		})
	}
}
