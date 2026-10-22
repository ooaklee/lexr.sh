package kernel

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
)

// TestObserveStubbleSelectionRetainsActualOrderAndSharedRoutes preserves stored
// order while keeping optional shared routes distinct from hardware claims.
func TestObserveStubbleSelectionRetainsActualOrderAndSharedRoutes(t *testing.T) {
	image, trees := stubbleObservationFixture(t)
	got, err := ObserveStubbleSelection(image, trees)
	if err != nil {
		t.Fatal(err)
	}
	if got.Policy != StubbleInventoryOrderPolicy || got.ImageSHA256 != stubbleDigest(image) ||
		!reflect.DeepEqual(got.PayloadOrder, []string{"fixture-zeta", "fixture-alpha"}) {
		t.Fatalf("actual order was not retained: %#v", got)
	}
	wantRecords := []StubbleHWIDRecord{
		{HWID: "00112233-4455-6677-8899-aabbccddeeff", Compatible: "vendor,zeta"},
		{HWID: "00112233-4455-6677-8899-aabbccddeeff", Compatible: "vendor,alpha"},
		{HWID: "00112233-4455-6677-8899-aabbccddeeff", Compatible: "vendor,zeta"},
	}
	if !reflect.DeepEqual(got.HWIDRecords, wantRecords) {
		t.Fatalf("ordered records = %#v", got.HWIDRecords)
	}
	if err := VerifyStubbleSelection(image, trees, got); err != nil {
		t.Fatal(err)
	}
	// The caller's inventory is device-sorted, not stored PE order.
	trees[0], trees[1] = trees[1], trees[0]
	if err := VerifyStubbleSelection(image, trees, got); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyStubbleSelectionRejectsChangedClaimsAndBytes binds every observation
// field and the complete image, including file-alignment bytes.
func TestVerifyStubbleSelectionRejectsChangedClaimsAndBytes(t *testing.T) {
	image, trees := stubbleObservationFixture(t)
	baseline, err := ObserveStubbleSelection(image, trees)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*StubbleSelection)
	}{
		{"policy", func(v *StubbleSelection) { v.Policy = "other" }},
		{"image digest", func(v *StubbleSelection) { v.ImageSHA256 = strings.Repeat("a", 64) }},
		{"table digest", func(v *StubbleSelection) { v.HWIDTableSHA256 = strings.Repeat("b", 64) }},
		{"payload order", func(v *StubbleSelection) { v.PayloadOrder[0], v.PayloadOrder[1] = v.PayloadOrder[1], v.PayloadOrder[0] }},
		{"HWID order", func(v *StubbleSelection) { v.HWIDRecords[0], v.HWIDRecords[1] = v.HWIDRecords[1], v.HWIDRecords[0] }},
		{"missing record", func(v *StubbleSelection) { v.HWIDRecords = v.HWIDRecords[:2] }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := *baseline
			value.PayloadOrder = append([]string(nil), baseline.PayloadOrder...)
			value.HWIDRecords = append([]StubbleHWIDRecord(nil), baseline.HWIDRecords...)
			test.mutate(&value)
			if err := VerifyStubbleSelection(image, trees, &value); err == nil {
				t.Fatal("accepted changed evidence")
			}
		})
	}
	changed := append([]byte(nil), image...)
	changed[len(changed)-1] ^= 1 // Alignment bytes still belong to the whole image digest.
	if err := VerifyStubbleSelection(changed, trees, baseline); err == nil {
		t.Fatal("accepted changed whole-image bytes")
	}
	if err := VerifyStubbleSelection(image, trees, nil); err == nil {
		t.Fatal("accepted missing selection")
	}
}

// TestObserveStubbleSelectionRejectsInventoryAndPEMismatch exercises malformed
// EFI headers, inventory disagreement and incomplete or duplicate sections.
func TestObserveStubbleSelectionRejectsInventoryAndPEMismatch(t *testing.T) {
	image, trees := stubbleObservationFixture(t)
	tests := []struct {
		name   string
		mutate func([]byte, []DeviceTree) ([]byte, []DeviceTree)
	}{
		{"not PE", func(_ []byte, v []DeviceTree) ([]byte, []DeviceTree) { return []byte("not PE"), v }},
		{"wrong machine", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			binary.LittleEndian.PutUint16(b[0x84:], pe.IMAGE_FILE_MACHINE_AMD64)
			return b, v
		}},
		{"COFF without EFI optional header", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			copy(b[0x98:], b[observationSectionTable:observationSectionTable+3*40])
			binary.LittleEndian.PutUint16(b[0x94:], 0)
			file, err := pe.NewFile(bytes.NewReader(b))
			if err != nil || file.OptionalHeader != nil {
				t.Fatalf("invalid COFF inverse fixture: %v", err)
			}
			file.Close()
			return b, v
		}},
		{"non-EFI subsystem", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			binary.LittleEndian.PutUint16(b[0x98+68:], pe.IMAGE_SUBSYSTEM_WINDOWS_CUI)
			return b, v
		}},
		{"zero entry point", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			binary.LittleEndian.PutUint32(b[0x98+16:], 0)
			return b, v
		}},
		{"entry point outside mapped image", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			binary.LittleEndian.PutUint32(b[0x98+16:], binary.LittleEndian.Uint32(b[0x98+56:]))
			return b, v
		}},
		{"mapped image above bound", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			binary.LittleEndian.PutUint32(b[0x98+56:], maximumStubbleImageBytes+1)
			return b, v
		}},
		{"headers outside image", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			binary.LittleEndian.PutUint32(b[0x98+60:], uint32(len(b)+1))
			return b, v
		}},
		{"missing inventory", func(b []byte, _ []DeviceTree) ([]byte, []DeviceTree) { return b, nil }},
		{"missing tree", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) { return b, v[:1] }},
		{"wrong digest", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			v[0].SHA256 = strings.Repeat("e", 64)
			return b, v
		}},
		{"wrong compatible", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			v[0].CompatibleStrings = []string{"vendor,other"}
			return b, v
		}},
		{"secondary compatible reordered", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			v[0].CompatibleStrings = []string{"qcom,fixture", "vendor,alpha"}
			return b, v
		}},
		{"duplicate device", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) { v[0].Device = v[1].Device; return b, v }},
		{"duplicate digest", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) { v[0].SHA256 = v[1].SHA256; return b, v }},
		{"unembedded tree", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) { v[0].EmbeddedMatches = 0; return b, v }},
		{"invalid match count", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) { v[0].EmbeddedMatches = 2; return b, v }},
		{"missing HWID section", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			copy(b[observationSectionTable+2*40:], []byte(".unused\x00"))
			return b, v
		}},
		{"multiple HWID sections", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			copy(b[observationSectionTable:], []byte(".hwids\x00\x00"))
			return b, v
		}},
		{"virtual larger than raw", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			binary.LittleEndian.PutUint32(b[observationSectionTable+8:], 1024)
			return b, v
		}},
		{"DTB above bound", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			binary.LittleEndian.PutUint32(b[observationSectionTable+8:], maximumStubbleDTBBytes+1)
			return b, v
		}},
		{"raw outside image", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			binary.LittleEndian.PutUint32(b[observationSectionTable+20:], uint32(len(b)))
			return b, v
		}},
		{"too many sections", func(b []byte, v []DeviceTree) ([]byte, []DeviceTree) {
			binary.LittleEndian.PutUint16(b[0x86:], maximumStubbleSections+1)
			return b, v
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyTrees := append([]DeviceTree(nil), trees...)
			b, v := test.mutate(append([]byte(nil), image...), copyTrees)
			if _, err := ObserveStubbleSelection(b, v); err == nil {
				t.Fatal("accepted invalid inventory or image")
			}
		})
	}
	first := observationFDT([]string{"vendor,zeta", "qcom,fixture"})
	table := observationHWIDs([]observationHWID{{1, "vendor,zeta"}})
	duplicate := observationPE([]observationSection{{".dtbauto", first}, {".dtbauto", first}, {".hwids", table}})
	if _, err := ObserveStubbleSelection(duplicate, trees); err == nil {
		t.Fatal("accepted duplicate payload sections")
	}
	missing := observationPE([]observationSection{{".dtbauto", first}, {".hwids", table}})
	if _, err := ObserveStubbleSelection(missing, trees); err == nil {
		t.Fatal("accepted missing declared payload")
	}
	alpha := observationFDT([]string{"vendor,alpha", "qcom,fixture"})
	duplicateTable := observationPE([]observationSection{{".dtbauto", first}, {".dtbauto", alpha}, {".hwids", table}, {".hwids", table}})
	if _, err := ObserveStubbleSelection(duplicateTable, trees); err == nil {
		t.Fatal("accepted two valid HWID tables")
	}
}

// TestStubbleHWIDRecordsRejectsMalformedTable checks packed-row, string and
// resource bounds without executing firmware or interpreting a board's CHIDs.
func TestStubbleHWIDRecordsRejectsMalformedTable(t *testing.T) {
	valid := observationHWIDs([]observationHWID{{1, "vendor,zeta"}, {2, "firmware-id"}})
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"too short", func(_ []byte) []byte { return []byte{0} }},
		{"above table bound", func(_ []byte) []byte { return make([]byte, maximumStubbleHWIDBytes+1) }},
		{"unknown type", func(b []byte) []byte { binary.LittleEndian.PutUint32(b, 0x3000001c); return b }},
		{"wrong row size", func(b []byte) []byte { binary.LittleEndian.PutUint32(b, 0x10000020); return b }},
		{"non-null terminator", func(b []byte) []byte { b[2*28+4] = 1; return b }},
		{"missing terminator", func(b []byte) []byte { return b[:2*28] }},
		{"offset into records", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[20:], 28); return b }},
		{"offset inside string", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[20:], binary.LittleEndian.Uint32(b[20:])+1)
			return b
		}},
		{"offset outside", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[24:], uint32(len(b))); return b }},
		{"invalid UTF8", func(b []byte) []byte { b[3*28] = 0xff; return b }},
		{"missing trailing null", func(b []byte) []byte { return b[:len(b)-1] }},
		{"invalid compatible", func(b []byte) []byte { at := binary.LittleEndian.Uint32(b[24:]); b[at] = '/'; return b }},
		{"only firmware records", func(_ []byte) []byte { return observationHWIDs([]observationHWID{{2, "firmware-id"}}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := stubbleHWIDRecords(test.mutate(append([]byte(nil), valid...))); err == nil {
				t.Fatal("accepted malformed HWID table")
			}
		})
	}
	many := make([]observationHWID, maximumStubbleHWIDRecords+1)
	for i := range many {
		many[i] = observationHWID{1, "vendor,zeta"}
	}
	if _, err := stubbleHWIDRecords(observationHWIDs(many)); err == nil {
		t.Fatal("accepted excessive HWID rows")
	}
}

// TestStubbleFDTCompatiblesRejectsMalformedTree keeps root-compatible evidence
// tied to the bounded, structurally valid payload rather than metadata claims.
func TestStubbleFDTCompatiblesRejectsMalformedTree(t *testing.T) {
	valid := observationFDT([]string{"vendor,zeta", "qcom,fixture"})
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"magic", func(b []byte) []byte { b[0] = 0; return b }},
		{"total outside", func(b []byte) []byte { binary.BigEndian.PutUint32(b[4:], uint32(len(b)+1)); return b }},
		{"structure outside", func(b []byte) []byte { binary.BigEndian.PutUint32(b[8:], uint32(len(b))); return b }},
		{"string overlap", func(b []byte) []byte { binary.BigEndian.PutUint32(b[12:], 56); return b }},
		{"reserve overlap", func(b []byte) []byte { binary.BigEndian.PutUint32(b[16:], 56); return b }},
		{"old header version", func(b []byte) []byte { binary.BigEndian.PutUint32(b[20:], 16); return b }},
		{"bad property name", func(b []byte) []byte { binary.BigEndian.PutUint32(b[56+8+8:], 0xffffffff); return b }},
		{"bad property length", func(b []byte) []byte { binary.BigEndian.PutUint32(b[56+8+4:], 0xffffffff); return b }},
		{"missing compatible", func(_ []byte) []byte { return observationFDT([]string{""}) }},
		{"duplicate compatible", func(_ []byte) []byte { return observationFDT([]string{"vendor,zeta", "vendor,zeta"}) }},
		{"noncanonical compatible", func(_ []byte) []byte { return observationFDT([]string{"Vendor,Zeta"}) }},
		{"unclosed root", func(b []byte) []byte {
			at := binary.BigEndian.Uint32(b[8:]) + binary.BigEndian.Uint32(b[36:]) - 8
			binary.BigEndian.PutUint32(b[at:], 4)
			return b
		}},
		{"unknown token", func(b []byte) []byte { binary.BigEndian.PutUint32(b[56:], 7); return b }},
		{"missing end", func(b []byte) []byte {
			at := binary.BigEndian.Uint32(b[8:]) + binary.BigEndian.Uint32(b[36:]) - 4
			binary.BigEndian.PutUint32(b[at:], 4)
			return b
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := stubbleFDTCompatibles(test.mutate(append([]byte(nil), valid...))); err == nil {
				t.Fatal("accepted malformed FDT")
			}
		})
	}
}

// TestStubblePEEnvelopeRejectsMetadataAmplification keeps envelope admission
// ahead of all section work and never decodes unused symbol/relocation tables.
func TestStubblePEEnvelopeRejectsMetadataAmplification(t *testing.T) {
	image, trees := stubbleObservationFixture(t)
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"bare COFF with EFI optional header", func(b []byte) []byte {
			const removed = 0x84
			b = append([]byte(nil), b[removed:]...)
			binary.LittleEndian.PutUint32(b[20+60:], binary.LittleEndian.Uint32(b[20+60:])-removed)
			for i := 0; i < 3; i++ {
				header := b[20+240+i*40:]
				binary.LittleEndian.PutUint32(header[20:], binary.LittleEndian.Uint32(header[20:])-removed)
			}
			file, err := pe.NewFile(bytes.NewReader(b))
			if err != nil {
				t.Fatalf("bare COFF fixture is not parseable by debug/pe: %v", err)
			}
			if _, ok := file.OptionalHeader.(*pe.OptionalHeader64); !ok {
				t.Fatal("bare COFF fixture lacks EFI optional header")
			}
			file.Close()
			return b
		}},
		{"missing MZ", func(b []byte) []byte { b[0] = 'X'; return b }},
		{"PE offset inside DOS header", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x3c:], 32); return b }},
		{"PE offset outside image", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x3c:], 0xffffffff); return b }},
		{"wrong PE signature", func(b []byte) []byte { b[0x80] = 'X'; return b }},
		{"zero sections", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[0x86:], 0); return b }},
		{"section table outside image", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[0x94:], 0xffff); return b }},
		{"section table outside headers", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x98+60:], 256); return b }},
		{"selected payload inside declared headers", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[0x98+60:], binary.LittleEndian.Uint32(b[0x98+60:])+1)
			return b
		}},
		{"short optional header", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[0x94:], 100); return b }},
		{"directory count overflow", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x98+108:], 0xffffffff); return b }},
		{"symbols without pointer", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x84+12:], 1); return b }},
		{"symbol count amplification", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[0x84+8:], 1024)
			binary.LittleEndian.PutUint32(b[0x84+12:], 0xffffffff)
			return b
		}},
		{"symbol table in headers", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x84+8:], 256); return b }},
		{"string table outside image", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[0x84+8:], uint32(len(b))); return b }},
		{"oversized string table", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[0x84+8:], uint32(len(b)-4))
			binary.LittleEndian.PutUint32(b[len(b)-4:], 0xffffffff)
			return b
		}},
		{"relocations without pointer", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[observationSectionTable+32:], 1); return b }},
		{"relocation count amplification", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[observationSectionTable+24:], uint32(len(b)-1))
			binary.LittleEndian.PutUint16(b[observationSectionTable+32:], 0xffff)
			return b
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ObserveStubbleSelection(test.mutate(append([]byte(nil), image...)), trees); err == nil {
				t.Fatal("accepted invalid envelope or metadata extent")
			}
		})
	}
}

// TestObserveStubbleSelectionRetainsBoundedUnusedCOFFData avoids rejecting
// legitimate bounded metadata solely because this observer does not consume it.
func TestObserveStubbleSelectionRetainsBoundedUnusedCOFFData(t *testing.T) {
	image, trees := stubbleObservationFixture(t)
	start := uint32(len(image))
	image = append(image, make([]byte, 18+4+10)...)
	copy(image[start:start+8], []byte("fixture\x00"))
	binary.LittleEndian.PutUint32(image[0x84+8:], start)
	binary.LittleEndian.PutUint32(image[0x84+12:], 1)
	binary.LittleEndian.PutUint32(image[start+18:], 4)
	binary.LittleEndian.PutUint32(image[observationSectionTable+24:], start+22)
	binary.LittleEndian.PutUint16(image[observationSectionTable+32:], 1)
	file, err := pe.NewFile(bytes.NewReader(image))
	if err != nil {
		t.Fatalf("bounded COFF metadata fixture is invalid: %v", err)
	}
	file.Close()
	if _, err := ObserveStubbleSelection(image, trees); err != nil {
		t.Fatalf("bounded unused COFF metadata was rejected: %v", err)
	}
}

// TestStubbleFDTRetainsCompatibleVersionsAndRejectsSlackAndLateProperties
// follows backward-compatible version headers while enforcing payload grammar.
func TestStubbleFDTRetainsCompatibleVersionsAndRejectsSlackAndLateProperties(t *testing.T) {
	valid := observationFDT([]string{"vendor,zeta", "qcom,fixture"})
	for _, version := range []struct {
		name string
		pair [2]uint32
	}{{"future-compatible-version", [2]uint32{18, 16}}, {"last-compatible-version17", [2]uint32{17, 17}}} {
		t.Run(version.name, func(t *testing.T) {
			value := append([]byte(nil), valid...)
			binary.BigEndian.PutUint32(value[20:], version.pair[0])
			binary.BigEndian.PutUint32(value[24:], version.pair[1])
			if _, err := stubbleFDTCompatibles(value); err != nil {
				t.Fatalf("backward-compatible version %v was rejected: %v", version.pair, err)
			}
		})
	}
	for _, trailing := range []struct {
		name  string
		value byte
	}{{"trailing-zero-byte", 0}, {"trailing-nonzero-byte", 'X'}} {
		t.Run(trailing.name, func(t *testing.T) {
			if _, err := stubbleFDTCompatibles(append(append([]byte(nil), valid...), trailing.value)); err == nil {
				t.Fatal("accepted bytes outside declared FDT totalsize")
			}
		})
	}
	structureSize := int(binary.BigEndian.Uint32(valid[36:]))
	structure := append([]byte(nil), valid[56:56+structureSize]...)
	stringsBlock := append([]byte(nil), valid[56+structureSize:]...)
	var child bytes.Buffer
	_ = binary.Write(&child, binary.BigEndian, uint32(1))
	child.Write([]byte{'c', 'h', 'i', 'l', 'd', 0, 0, 0})
	_ = binary.Write(&child, binary.BigEndian, uint32(2))
	late := append(append(append([]byte(nil), structure[:8]...), child.Bytes()...), structure[8:]...)
	t.Run("property-after-subnode", func(t *testing.T) {
		if _, err := stubbleFDTCompatibles(observationRewriteFDT(valid, late, stringsBlock)); err == nil {
			t.Fatal("accepted root property after child")
		}
	})
	// A long non-compatible property must not trigger repeated unbounded scans
	// of the strings block. The limit applies to every property name.
	var property bytes.Buffer
	for _, number := range []uint32{3, 0, uint32(len(stringsBlock))} {
		_ = binary.Write(&property, binary.BigEndian, number)
	}
	long := append(append(append([]byte(nil), structure[:len(structure)-8]...), property.Bytes()...), structure[len(structure)-8:]...)
	longStrings := append(append([]byte(nil), stringsBlock...), []byte(strings.Repeat("x", 256)+"\x00")...)
	t.Run("bounded-property-name", func(t *testing.T) {
		if _, err := stubbleFDTCompatibles(observationRewriteFDT(valid, long, longStrings)); err == nil {
			t.Fatal("accepted oversized property name")
		}
	})
}

// observationRewriteFDT preserves the reservation/header layout while replacing
// only bounded structure and string blocks for grammar regression fixtures.
func observationRewriteFDT(original, structure, stringsBlock []byte) []byte {
	value := append([]byte(nil), original[:56]...)
	binary.BigEndian.PutUint32(value[4:], uint32(56+len(structure)+len(stringsBlock)))
	binary.BigEndian.PutUint32(value[12:], uint32(56+len(structure)))
	binary.BigEndian.PutUint32(value[32:], uint32(len(stringsBlock)))
	binary.BigEndian.PutUint32(value[36:], uint32(len(structure)))
	value = append(value, structure...)
	return append(value, stringsBlock...)
}

// stubbleObservationFixture supplies a deliberately non-device-sorted image
// and a shared HWID with an interspersed firmware-only row.
func stubbleObservationFixture(t *testing.T) ([]byte, []DeviceTree) {
	t.Helper()
	zetaCompat, alphaCompat := []string{"vendor,zeta", "qcom,fixture"}, []string{"vendor,alpha", "qcom,fixture"}
	zeta, alpha := observationFDT(zetaCompat), observationFDT(alphaCompat)
	table := observationHWIDs([]observationHWID{{1, "vendor,zeta"}, {2, "firmware-id"}, {1, "vendor,alpha"}, {1, "vendor,zeta"}})
	image := observationPE([]observationSection{{".dtbauto", zeta}, {".dtbauto", alpha}, {".hwids", table}})
	trees := []DeviceTree{
		{Device: "fixture-alpha", SHA256: stubbleDigest(alpha), CompatibleStrings: alphaCompat, EmbeddedMatches: 1},
		{Device: "fixture-zeta", SHA256: stubbleDigest(zeta), CompatibleStrings: zetaCompat, EmbeddedMatches: 1},
	}
	return image, trees
}

// observationSection supplies one named virtual payload to the PE fixture.
type observationSection struct {
	name string
	data []byte
}

// observationSectionTable follows the fixture's complete PE32+ optional header.
const observationSectionTable = 0x98 + 240

// observationPE builds only the PE fields needed for data observation; it is
// not executable firmware and makes no physical boot or signature claim.
func observationPE(sections []observationSection) []byte {
	headerBytes := observationSectionTable + len(sections)*40
	offset := (headerBytes + 511) &^ 511
	image := make([]byte, offset)
	copy(image, "MZ")
	binary.LittleEndian.PutUint32(image[0x3c:], 0x80)
	copy(image[0x80:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(image[0x84:], pe.IMAGE_FILE_MACHINE_ARM64)
	binary.LittleEndian.PutUint16(image[0x86:], uint16(len(sections)))
	binary.LittleEndian.PutUint16(image[0x94:], 240)
	binary.LittleEndian.PutUint16(image[0x98:], 0x20b)
	binary.LittleEndian.PutUint32(image[0x98+16:], 0x1000)
	binary.LittleEndian.PutUint32(image[0x98+32:], 0x1000)
	binary.LittleEndian.PutUint32(image[0x98+36:], 512)
	binary.LittleEndian.PutUint32(image[0x98+56:], uint32((len(sections)+1)*0x1000))
	binary.LittleEndian.PutUint32(image[0x98+60:], uint32(offset))
	binary.LittleEndian.PutUint16(image[0x98+68:], pe.IMAGE_SUBSYSTEM_EFI_APPLICATION)
	binary.LittleEndian.PutUint32(image[0x98+108:], 16)
	for i, section := range sections {
		header := image[observationSectionTable+i*40 : observationSectionTable+(i+1)*40]
		copy(header, section.name)
		binary.LittleEndian.PutUint32(header[8:], uint32(len(section.data)))
		binary.LittleEndian.PutUint32(header[12:], uint32((i+1)*0x1000))
		rawBytes := (len(section.data) + 511) &^ 511
		binary.LittleEndian.PutUint32(header[16:], uint32(rawBytes))
		binary.LittleEndian.PutUint32(header[20:], uint32(offset))
		image = append(image, section.data...)
		image = append(image, make([]byte, rawBytes-len(section.data))...)
		offset += rawBytes
	}
	return image
}

// observationHWID supplies a devicetree or firmware row to the packed fixture.
type observationHWID struct {
	typeID uint32
	value  string
}

// observationHWIDs retains caller order and uses a known little-endian GUID.
func observationHWIDs(rows []observationHWID) []byte {
	data := make([]byte, (len(rows)+1)*28)
	offsets := map[string]uint32{}
	appendString := func(value string) uint32 {
		if offset, exists := offsets[value]; exists {
			return offset
		}
		offset := uint32(len(data))
		offsets[value] = offset
		data = append(data, []byte(value)...)
		data = append(data, 0)
		return offset
	}
	for i, row := range rows {
		name, value := appendString("Fixture device"), appendString(row.value)
		at := i * 28
		binary.LittleEndian.PutUint32(data[at:], row.typeID<<28|28)
		copy(data[at+4:at+20], []byte{0x33, 0x22, 0x11, 0x00, 0x55, 0x44, 0x77, 0x66, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
		binary.LittleEndian.PutUint32(data[at+20:], name)
		binary.LittleEndian.PutUint32(data[at+24:], value)
	}
	return data
}

// observationFDT creates a version-17 root with exact compatible-string order.
func observationFDT(compatibles []string) []byte {
	value := []byte(strings.Join(compatibles, "\x00") + "\x00")
	var structure bytes.Buffer
	for _, number := range []uint32{1, 0, 3, uint32(len(value)), 0} {
		_ = binary.Write(&structure, binary.BigEndian, number)
	}
	structure.Write(value)
	for structure.Len()%4 != 0 {
		structure.WriteByte(0)
	}
	_ = binary.Write(&structure, binary.BigEndian, uint32(2))
	_ = binary.Write(&structure, binary.BigEndian, uint32(9))
	stringsBlock := []byte("compatible\x00")
	data := make([]byte, 56)
	for offset, number := range map[int]uint32{0: 0xd00dfeed, 4: uint32(56 + structure.Len() + len(stringsBlock)), 8: 56, 12: uint32(56 + structure.Len()), 16: 40, 20: 17, 24: 16, 32: uint32(len(stringsBlock)), 36: uint32(structure.Len())} {
		binary.BigEndian.PutUint32(data[offset:], number)
	}
	data = append(data, structure.Bytes()...)
	return append(data, stringsBlock...)
}
