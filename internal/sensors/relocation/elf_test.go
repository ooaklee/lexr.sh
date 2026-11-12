package relocation

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
)

// syntheticELF is deliberately just a small dynamic-table fixture, not a
// source-build output and never accepted through the opaque package API.
func syntheticELF(soname string, needed ...string) []byte {
	stringsTable := []byte{0}
	type entry struct {
		tag    uint64
		offset uint64
	}
	var entries []entry
	add := func(tag uint64, name string) {
		entries = append(entries, entry{tag, uint64(len(stringsTable))})
		stringsTable = append(stringsTable, append([]byte(name), 0)...)
	}
	for _, name := range needed {
		add(1, name)
	}
	if soname != "" {
		add(14, soname)
	}
	entries = append(entries, entry{})
	names := []byte("\x00.dynstr\x00.dynamic\x00.shstrtab\x00")
	dynamicOffset := (64 + len(stringsTable) + 7) &^ 7
	namesOffset := dynamicOffset + 16*len(entries)
	sectionsOffset := (namesOffset + len(names) + 7) &^ 7
	data := make([]byte, sectionsOffset+4*64)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	b := binary.LittleEndian
	b.PutUint16(data[16:18], 3)
	b.PutUint16(data[18:20], 183)
	b.PutUint32(data[20:24], 1)
	b.PutUint64(data[40:48], uint64(sectionsOffset))
	b.PutUint16(data[52:54], 64)
	b.PutUint16(data[58:60], 64)
	b.PutUint16(data[60:62], 4)
	b.PutUint16(data[62:64], 3)
	copy(data[64:], stringsTable)
	for i, entry := range entries {
		b.PutUint64(data[dynamicOffset+i*16:], entry.tag)
		b.PutUint64(data[dynamicOffset+i*16+8:], entry.offset)
	}
	copy(data[namesOffset:], names)
	section := func(index, name, typ, offset, size, link, entrySize int) {
		header := data[sectionsOffset+index*64:]
		b.PutUint32(header, uint32(name))
		b.PutUint32(header[4:], uint32(typ))
		b.PutUint64(header[24:], uint64(offset))
		b.PutUint64(header[32:], uint64(size))
		b.PutUint32(header[40:], uint32(link))
		b.PutUint64(header[48:], 1)
		b.PutUint64(header[56:], uint64(entrySize))
	}
	section(1, 1, 3, 64, len(stringsTable), 0, 0)
	section(2, 9, 6, dynamicOffset, 16*len(entries), 1, 16)
	section(3, 18, 3, namesOffset, len(names), 0, 0)
	return data
}

// TestSyntheticELFContext checks direct dependency and SONAME interpretation.
func TestSyntheticELFContext(t *testing.T) {
	for _, name := range elfPaths() {
		soname := ""
		if name == "lib/libqmi-glib.so.5.12.0" {
			soname = "libqmi-glib.so.5"
		}
		if name == "lib/libssc.so.2" {
			soname = "libssc.so.2"
		}
		context, err := inspectContext(name, syntheticELF(soname, "libc.so.6"))
		if err != nil || len(context.needed) != 1 || context.needed[0] != "libc.so.6" || context.soname != soname {
			t.Fatalf("synthetic context %s: %#v %v", name, context, err)
		}
	}
	for _, test := range []struct {
		name, soname string
		needed       []string
	}{
		{"bin/ssccli", "libunexpected.so", []string{"libc.so.6"}},
		{"lib/libssc.so.2", "libssc.so.3", []string{"libc.so.6"}},
		{"lib/libqmi-glib.so.5.12.0", "", []string{"libc.so.6"}},
		{"bin/ssccli", "", nil},
		{"bin/ssccli", "", []string{"libc.so.6", "libc.so.6"}},
		{"bin/ssccli", "", []string{"../libbad.so"}},
	} {
		if _, err := inspectContext(test.name, syntheticELF(test.soname, test.needed...)); err == nil {
			t.Fatalf("accepted synthetic invalid dynamic context: %#v", test)
		}
	}
	wrongArchitecture := syntheticELF("", "libc.so.6")
	binary.LittleEndian.PutUint16(wrongArchitecture[18:20], 62)
	if _, err := inspectContext("bin/ssccli", wrongArchitecture); err == nil {
		t.Fatal("accepted synthetic x86-64 ELF")
	}
}

// syntheticPayloadObservation combines invented bytes and data without native
// execution or a forged opaque verified-package handle.
func syntheticPayloadObservation() (map[string][]byte, Observation) {
	files := map[string][]byte{}
	for name := range expectedFiles() {
		files[name] = []byte("synthetic licence fixture")
	}
	files["lib/libqmi-glib.so.5.12.0"] = syntheticELF("libqmi-glib.so.5", "libc.so.6")
	files["lib/libssc.so.2"] = syntheticELF("libssc.so.2", "libqmi-glib.so.5", "libc.so.6")
	files["bin/ssccli"] = syntheticELF("", "libssc.so.2", "libc.so.6")
	files["libexec/iio-sensor-proxy"] = syntheticELF("", "libssc.so.2", "libc.so.6")
	files["bin/monitor-sensor"] = syntheticELF("", "libc.so.6")
	observation := syntheticObservation()
	for name, data := range files {
		digest := sha256.Sum256(data)
		observation.FileSHA256[name] = hex.EncodeToString(digest[:])
	}
	for _, name := range elfPaths() {
		observation.Listings[name] = syntheticListing(name)
	}
	return files, observation
}

// TestSyntheticWholeObservationClosure checks the complete byte-to-data binding.
func TestSyntheticWholeObservationClosure(t *testing.T) {
	files, observation := syntheticPayloadObservation()
	if err := verifyPayload(files, expectedLinks(), observation); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Observation){
		"schema":       func(o *Observation) { o.Schema = "wrong" },
		"architecture": func(o *Observation) { o.Architecture = "x86_64" },
		"prefix":       func(o *Observation) { o.Prefix = "/tmp/providers" },
		"missing hash": func(o *Observation) { delete(o.FileSHA256, "bin/ssccli") },
		"extra hash":   func(o *Observation) { o.FileSHA256["other"] = strings.Repeat("a", 64) },
		"wrong hash":   func(o *Observation) { o.FileSHA256["bin/ssccli"] = strings.Repeat("a", 64) },
		"uppercase hash": func(o *Observation) {
			o.FileSHA256["bin/ssccli"] = strings.ToUpper(o.FileSHA256["bin/ssccli"])
		},
		"missing alias": func(o *Observation) { delete(o.Links, "lib/libssc.so") },
		"extra alias":   func(o *Observation) { o.Links["lib/extra"] = "libssc.so.2" },
		"alias target":  func(o *Observation) { o.Links["lib/libssc.so"] = "/usr/lib/libssc.so.2" },
		"missing listing": func(o *Observation) {
			delete(o.Listings, "bin/ssccli")
		},
		"extra listing": func(o *Observation) { o.Listings["other"] = "unexpected" },
		"replacement listing": func(o *Observation) {
			delete(o.Listings, "bin/ssccli")
			o.Listings["other"] = syntheticListing("bin/ssccli")
		},
		"wrong role listing": func(o *Observation) {
			o.Listings["bin/ssccli"] = syntheticListing("bin/monitor-sensor")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			files, observation := syntheticPayloadObservation()
			mutate(&observation)
			if err := verifyPayload(files, expectedLinks(), observation); err == nil {
				t.Fatal("accepted invalid synthetic whole observation")
			}
		})
	}
}
